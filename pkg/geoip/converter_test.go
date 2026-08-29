package geoip

import (
	"bytes"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/CoderQuinn/ForgeRules/proto"
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
