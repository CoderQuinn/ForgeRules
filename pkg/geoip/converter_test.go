package geoip

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/CoderQuinn/ForgeRules/proto"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"
	"google.golang.org/protobuf/proto"
)

func TestDatToMMDBWithBuildEpochIsDeterministic(t *testing.T) {
	t.Parallel()

	input := &pb.GeoIPList{Entry: []*pb.GeoIP{
		{
			CountryCode: "US",
			Cidr:        []*pb.CIDR{testCIDR("8.8.8.0/24")},
		},
	}}
	data, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	tempDir := t.TempDir()
	datPath := filepath.Join(tempDir, "geoip.dat")
	if err := os.WriteFile(datPath, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	const buildEpoch int64 = 1_700_000_000
	options := MMDBOptions{BuildEpoch: buildEpoch}
	firstPath := filepath.Join(tempDir, "first.mmdb")
	secondPath := filepath.Join(tempDir, "second.mmdb")
	if err := DatToMMDBWithOptions(datPath, firstPath, options); err != nil {
		t.Fatalf("first conversion: %v", err)
	}
	if err := DatToMMDBWithOptions(datPath, secondPath, options); err != nil {
		t.Fatalf("second conversion: %v", err)
	}

	directory := os.DirFS(tempDir)
	first, err := fs.ReadFile(directory, "first.mmdb")
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}
	second, err := fs.ReadFile(directory, "second.mmdb")
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("fixed build epoch produced different MMDB bytes")
	}

	database, err := maxminddb.Open(firstPath)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close output: %v", err)
		}
	})
	if database.Metadata.BuildEpoch != uint(buildEpoch) {
		t.Errorf("build epoch = %d, want %d", database.Metadata.BuildEpoch, buildEpoch)
	}
}

func TestDatToMMDBRejectsNegativeBuildEpoch(t *testing.T) {
	t.Parallel()

	err := DatToMMDBWithOptions("unused.dat", "unused.mmdb", MMDBOptions{BuildEpoch: -1})
	if err == nil {
		t.Fatal("expected negative build epoch to fail")
	}
}

func TestDatToMMDBRejectsInvalidCIDRWithoutReplacingOutput(t *testing.T) {
	t.Parallel()

	input := &pb.GeoIPList{Entry: []*pb.GeoIP{{
		CountryCode: "US",
		Cidr: []*pb.CIDR{
			testCIDR("8.8.8.0/24"),
			{Ip: []byte{1, 2, 3}, Prefix: 24},
		},
	}}}
	tempDir := t.TempDir()
	datPath := writeGeoIPFixture(t, tempDir, input)
	outputPath := filepath.Join(tempDir, "geoip.mmdb")
	const lastKnownGood = "last-known-good"
	if err := os.WriteFile(outputPath, []byte(lastKnownGood), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	err := DatToMMDB(datPath, outputPath)
	if err == nil {
		t.Fatal("expected invalid CIDR to fail")
	}
	if !strings.Contains(err.Error(), `geoip entry 0 ("US") CIDR 1`) {
		t.Errorf("error lacks source context: %v", err)
	}
	if actual := string(readGeoIPOutput(t, tempDir)); actual != lastKnownGood {
		t.Errorf("output = %q, want preserved %q", actual, lastKnownGood)
	}
	freshOutputPath := filepath.Join(tempDir, "fresh.mmdb")
	if err := DatToMMDB(datPath, freshOutputPath); err == nil {
		t.Fatal("expected invalid CIDR to fail for a fresh output")
	}
	if _, err := os.Stat(freshOutputPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid CIDR published a fresh output, stat error = %v", err)
	}
}

func TestDatToMMDBFailsClosedOnWriterInsertError(t *testing.T) {
	t.Parallel()

	input := &pb.GeoIPList{Entry: []*pb.GeoIP{{
		CountryCode: "US",
		Cidr:        []*pb.CIDR{testCIDR("8.8.8.0/24")},
	}}}
	tempDir := t.TempDir()
	datPath := writeGeoIPFixture(t, tempDir, input)
	outputPath := filepath.Join(tempDir, "geoip.mmdb")
	const lastKnownGood = "last-known-good"
	if err := os.WriteFile(outputPath, []byte(lastKnownGood), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	writeCalled := false
	err := datToMMDBWithOptions(datPath, outputPath, MMDBOptions{}, func(mmdbwriter.Options) (mmdbTree, error) {
		return &stubMMDBTree{
			insert: func(*net.IPNet, mmdbtype.DataType) error {
				return errors.New("injected insert failure")
			},
			writeTo: func(io.Writer) (int64, error) {
				writeCalled = true
				return 0, nil
			},
		}, nil
	})
	if err == nil {
		t.Fatal("expected writer insert error to fail")
	}
	if !strings.Contains(err.Error(), "injected insert failure") {
		t.Errorf("error = %v", err)
	}
	if writeCalled {
		t.Error("writer was published after an insert failure")
	}
	if actual := string(readGeoIPOutput(t, tempDir)); actual != lastKnownGood {
		t.Errorf("output = %q, want preserved %q", actual, lastKnownGood)
	}
}

func TestDatToMMDBPreservesOutputOnWriteError(t *testing.T) {
	t.Parallel()

	input := &pb.GeoIPList{Entry: []*pb.GeoIP{{
		CountryCode: "US",
		Cidr:        []*pb.CIDR{testCIDR("8.8.8.0/24")},
	}}}
	tempDir := t.TempDir()
	datPath := writeGeoIPFixture(t, tempDir, input)
	outputPath := filepath.Join(tempDir, "geoip.mmdb")
	const lastKnownGood = "last-known-good"
	if err := os.WriteFile(outputPath, []byte(lastKnownGood), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	err := datToMMDBWithOptions(datPath, outputPath, MMDBOptions{}, func(mmdbwriter.Options) (mmdbTree, error) {
		return &stubMMDBTree{
			insert: func(*net.IPNet, mmdbtype.DataType) error { return nil },
			writeTo: func(io.Writer) (int64, error) {
				return 0, errors.New("injected write failure")
			},
		}, nil
	})
	if err == nil {
		t.Fatal("expected writer output error to fail")
	}
	if actual := string(readGeoIPOutput(t, tempDir)); actual != lastKnownGood {
		t.Errorf("output = %q, want preserved %q", actual, lastKnownGood)
	}
	matches, globErr := filepath.Glob(filepath.Join(tempDir, ".geoip.mmdb.*"))
	if globErr != nil {
		t.Fatalf("find temporary outputs: %v", globErr)
	}
	if len(matches) != 0 {
		t.Errorf("temporary outputs were not removed: %v", matches)
	}
}

func TestDatToMMDBRejectsUnreadableAndMalformedInput(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "geoip.mmdb")
	if err := DatToMMDB(filepath.Join(tempDir, "missing.dat"), outputPath); err == nil {
		t.Fatal("expected missing input to fail")
	}
	malformedPath := filepath.Join(tempDir, "malformed.dat")
	if err := os.WriteFile(malformedPath, []byte{0xff}, 0o600); err != nil {
		t.Fatalf("write malformed input: %v", err)
	}
	if err := DatToMMDB(malformedPath, outputPath); err == nil {
		t.Fatal("expected malformed protobuf to fail")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed conversions published an output, stat error = %v", err)
	}
}

func TestDatToMMDBReportsWriterCreationAndOutputPathErrors(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	datPath := writeGeoIPFixture(t, tempDir, &pb.GeoIPList{})
	err := datToMMDBWithOptions(datPath, filepath.Join(tempDir, "unused.mmdb"), MMDBOptions{}, func(mmdbwriter.Options) (mmdbTree, error) {
		return nil, errors.New("injected creation failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected creation failure") {
		t.Fatalf("writer creation error = %v", err)
	}

	missingParentOutput := filepath.Join(tempDir, "missing", "geoip.mmdb")
	if err := DatToMMDB(datPath, missingParentOutput); err == nil {
		t.Fatal("expected missing output directory to fail")
	}

	directoryTarget := filepath.Join(tempDir, "directory-target")
	if err := os.Mkdir(directoryTarget, 0o700); err != nil {
		t.Fatalf("create directory target: %v", err)
	}
	writer := &stubMMDBTree{
		insert: func(*net.IPNet, mmdbtype.DataType) error { return nil },
		writeTo: func(destination io.Writer) (int64, error) {
			written, err := destination.Write([]byte("mmdb"))
			return int64(written), err
		},
	}
	if err := writeMMDBAtomically(directoryTarget, writer); err == nil {
		t.Fatal("expected replacement of a directory to fail")
	}
}

func TestParseCIDRRejectsNilAndInvalidPrefix(t *testing.T) {
	t.Parallel()

	if _, err := parseCIDR(nil); err == nil {
		t.Fatal("expected nil CIDR to fail")
	}
	if _, err := parseCIDR(&pb.CIDR{
		Ip:     netip.MustParseAddr("192.0.2.1").AsSlice(),
		Prefix: 33,
	}); err == nil {
		t.Fatal("expected invalid IPv4 prefix to fail")
	}
}

func TestDatToMMDBIncludesReservedNetworks(t *testing.T) {
	t.Parallel()

	input := &pb.GeoIPList{Entry: []*pb.GeoIP{
		{
			CountryCode: "PRIVATE",
			Cidr:        []*pb.CIDR{testCIDR("10.0.0.0/8")},
		},
		{
			CountryCode: "LOOPBACK",
			Cidr:        []*pb.CIDR{testCIDR("127.0.0.0/8")},
		},
		{
			CountryCode: "LINKLOCAL",
			Cidr:        []*pb.CIDR{testCIDR("169.254.0.0/16")},
		},
		{
			CountryCode: "DOCUMENTATION",
			Cidr: []*pb.CIDR{
				testCIDR("192.0.2.0/24"),
				testCIDR("2001:db8::/32"),
			},
		},
		{
			CountryCode: "ULA",
			Cidr:        []*pb.CIDR{testCIDR("fc00::/7")},
		},
		{
			CountryCode: "US",
			Cidr:        []*pb.CIDR{testCIDR("8.0.0.0/8")},
		},
	}}

	data, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	tempDir := t.TempDir()
	datPath := filepath.Join(tempDir, "geoip.dat")
	mmdbPath := filepath.Join(tempDir, "geoip.mmdb")
	if err := os.WriteFile(datPath, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	if err := DatToMMDB(datPath, mmdbPath); err != nil {
		t.Fatalf("convert input: %v", err)
	}

	database, err := maxminddb.Open(mmdbPath)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close output: %v", err)
		}
	})

	assertCountryCode(t, database, "10.1.2.3", "PRIVATE")
	assertCountryCode(t, database, "127.0.0.1", "LOOPBACK")
	assertCountryCode(t, database, "169.254.10.20", "LINKLOCAL")
	assertCountryCode(t, database, "192.0.2.42", "DOCUMENTATION")
	assertCountryCode(t, database, "2001:db8::42", "DOCUMENTATION")
	assertCountryCode(t, database, "fd00::1", "ULA")
	assertCountryCode(t, database, "8.8.8.8", "US")
}

func testCIDR(value string) *pb.CIDR {
	prefix := netip.MustParsePrefix(value)
	return &pb.CIDR{
		Ip:     prefix.Addr().AsSlice(),
		Prefix: uint32(prefix.Bits()),
	}
}

func assertCountryCode(t *testing.T, database *maxminddb.Reader, ip, expected string) {
	t.Helper()

	result := database.Lookup(netip.MustParseAddr(ip))
	if !result.Found() {
		t.Fatalf("expected %s to be present in output database", ip)
	}

	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := result.Decode(&record); err != nil {
		t.Fatalf("decode record for %s: %v", ip, err)
	}
	if record.Country.ISOCode != expected {
		t.Errorf("country code for %s = %q, want %q", ip, record.Country.ISOCode, expected)
	}
}

type stubMMDBTree struct {
	insert  func(*net.IPNet, mmdbtype.DataType) error
	writeTo func(io.Writer) (int64, error)
}

func (tree *stubMMDBTree) Insert(network *net.IPNet, value mmdbtype.DataType) error {
	return tree.insert(network, value)
}

func (tree *stubMMDBTree) WriteTo(writer io.Writer) (int64, error) {
	return tree.writeTo(writer)
}

func writeGeoIPFixture(t *testing.T, directory string, input *pb.GeoIPList) string {
	t.Helper()
	data, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	path := filepath.Join(directory, "geoip.dat")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return path
}

func readGeoIPOutput(t *testing.T, directory string) []byte {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS(directory), "geoip.mmdb")
	if err != nil {
		t.Fatalf("read geoip.mmdb: %v", err)
	}
	return data
}
