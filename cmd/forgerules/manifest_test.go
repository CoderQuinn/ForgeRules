package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CoderQuinn/ForgeRules/pkg/geoip"
	"github.com/CoderQuinn/ForgeRules/pkg/geosite"
	pb "github.com/CoderQuinn/ForgeRules/proto"
	"google.golang.org/protobuf/proto"
)

func TestWriteBuildMetadataIsDeterministicAndSelfVerifying(t *testing.T) {
	t.Parallel()

	lockContents := []byte(validSourceLockJSON())
	lock, err := decodeSourceLock(bytes.NewReader(lockContents))
	if err != nil {
		t.Fatalf("decode source lock: %v", err)
	}
	root := t.TempDir()
	lockPath := filepath.Join(root, "input-lock.json")
	if err := os.WriteFile(lockPath, lockContents, 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}

	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, directory := range []string{first, second} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("create output directory: %v", err)
		}
		writeTestArtifact(t, directory, "official_geosite.json", []byte("geosite"))
		writeTestArtifact(t, directory, "official_geoip.mmdb", []byte("geoip"))
		if err := writeBuildMetadata(
			directory,
			lockPath,
			lock,
			strings.Repeat("c", 40),
			"go1.24.12",
		); err != nil {
			t.Fatalf("write build metadata: %v", err)
		}
		verifyChecksumFile(t, directory)
	}

	for _, filename := range []string{
		checksumsFilename,
		publishedSourceLockFilename,
		releaseManifestFilename,
	} {
		firstContents := readTestFile(t, first, filename)
		secondContents := readTestFile(t, second, filename)
		if !bytes.Equal(firstContents, secondContents) {
			t.Errorf("%s differs across identical builds", filename)
		}
	}

	var manifest releaseManifest
	if err := json.Unmarshal(readTestFile(t, first, releaseManifestFilename), &manifest); err != nil {
		t.Fatalf("decode release manifest: %v", err)
	}
	if manifest.Converter.Revision != strings.Repeat("c", 40) {
		t.Errorf("converter revision = %q", manifest.Converter.Revision)
	}
	if manifest.SourceLock.SHA256 != sha256Hex(lockContents) {
		t.Errorf("source lock SHA-256 = %q", manifest.SourceLock.SHA256)
	}
	if len(manifest.Bundles) != 1 || manifest.Bundles[0].GeoIP.BuildEpoch != 1_700_000_000 {
		t.Errorf("manifest bundles = %#v", manifest.Bundles)
	}
}

func TestWriteBuildMetadataRejectsUnknownRevision(t *testing.T) {
	t.Parallel()

	err := writeBuildMetadata(t.TempDir(), "unused.json", sourceLock{}, "unknown", "go1.24.12")
	if err == nil {
		t.Fatal("expected invalid converter revision to fail")
	}
}

func TestWriteBuildMetadataRejectsEmptyGoVersionAndUnreadableLock(t *testing.T) {
	t.Parallel()

	revision := strings.Repeat("a", 40)
	if err := writeBuildMetadata(t.TempDir(), "unused.json", sourceLock{}, revision, "  "); err == nil {
		t.Fatal("expected empty Go version to fail")
	}
	if err := writeBuildMetadata(t.TempDir(), "missing.json", sourceLock{}, revision, "go1.24.12"); err == nil {
		t.Fatal("expected missing source lock to fail")
	}
	if err := writeBuildMetadata(t.TempDir(), t.TempDir(), sourceLock{}, revision, "go1.24.12"); err == nil {
		t.Fatal("expected directory source lock to fail")
	}
}

func TestWriteBuildMetadataReportsMissingArtifactsWithoutPublishingManifest(t *testing.T) {
	t.Parallel()

	lockContents := []byte(validSourceLockJSON())
	lock, err := decodeSourceLock(bytes.NewReader(lockContents))
	if err != nil {
		t.Fatalf("decode source lock: %v", err)
	}
	root := t.TempDir()
	lockPath := filepath.Join(root, "input-lock.json")
	if err := os.WriteFile(lockPath, lockContents, 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}
	revision := strings.Repeat("b", 40)

	missingGeoSiteOutput := filepath.Join(root, "missing-geosite")
	if err := os.Mkdir(missingGeoSiteOutput, 0o700); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	if err := writeBuildMetadata(missingGeoSiteOutput, lockPath, lock, revision, "go1.24.12"); err == nil {
		t.Fatal("expected missing geosite artifact to fail")
	}
	if _, err := os.Stat(filepath.Join(missingGeoSiteOutput, releaseManifestFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed build published manifest, stat error = %v", err)
	}

	missingGeoIPOutput := filepath.Join(root, "missing-geoip")
	if err := os.Mkdir(missingGeoIPOutput, 0o700); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	writeTestArtifact(t, missingGeoIPOutput, "official_geosite.json", []byte("geosite"))
	if err := writeBuildMetadata(missingGeoIPOutput, lockPath, lock, revision, "go1.24.12"); err == nil {
		t.Fatal("expected missing geoip artifact to fail")
	}
	if _, err := os.Stat(filepath.Join(missingGeoIPOutput, releaseManifestFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed build published manifest, stat error = %v", err)
	}
}

func TestWriteBuildMetadataReportsOutputAndAtomicReplacementErrors(t *testing.T) {
	t.Parallel()

	lockContents := []byte(validSourceLockJSON())
	lock, err := decodeSourceLock(bytes.NewReader(lockContents))
	if err != nil {
		t.Fatalf("decode source lock: %v", err)
	}
	root := t.TempDir()
	lockPath := filepath.Join(root, "input-lock.json")
	if err := os.WriteFile(lockPath, lockContents, 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}
	if err := writeBuildMetadata(
		filepath.Join(root, "missing-output"),
		lockPath,
		lock,
		strings.Repeat("c", 40),
		"go1.24.12",
	); err == nil {
		t.Fatal("expected missing output directory to fail")
	}

	directoryTarget := filepath.Join(root, "directory-target")
	if err := os.Mkdir(directoryTarget, 0o700); err != nil {
		t.Fatalf("create directory target: %v", err)
	}
	if err := writeFileAtomically(directoryTarget, []byte("contents"), 0o644); err == nil {
		t.Fatal("expected atomic replacement of a directory to fail")
	}
	if err := writeFileAtomically(
		filepath.Join(root, "missing", "target"),
		[]byte("contents"),
		0o644,
	); err == nil {
		t.Fatal("expected missing atomic target directory to fail")
	}
}

func TestWriteBuildMetadataReportsManifestAndChecksumPublicationErrors(t *testing.T) {
	t.Parallel()

	lockContents := []byte(validSourceLockJSON())
	lock, err := decodeSourceLock(bytes.NewReader(lockContents))
	if err != nil {
		t.Fatalf("decode source lock: %v", err)
	}
	root := t.TempDir()
	lockPath := filepath.Join(root, "input-lock.json")
	if err := os.WriteFile(lockPath, lockContents, 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}
	revision := strings.Repeat("d", 40)

	for _, blockedFilename := range []string{releaseManifestFilename, checksumsFilename} {
		blockedFilename := blockedFilename
		t.Run(blockedFilename, func(t *testing.T) {
			t.Parallel()
			output := filepath.Join(root, blockedFilename+"-output")
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatalf("create output directory: %v", err)
			}
			writeTestArtifact(t, output, "official_geosite.json", []byte("geosite"))
			writeTestArtifact(t, output, "official_geoip.mmdb", []byte("geoip"))
			if err := os.Mkdir(filepath.Join(output, blockedFilename), 0o700); err != nil {
				t.Fatalf("create blocking directory: %v", err)
			}
			if err := writeBuildMetadata(output, lockPath, lock, revision, "go1.24.12"); err == nil {
				t.Fatalf("expected blocked %s publication to fail", blockedFilename)
			}
		})
	}
}

func TestDigestFileRejectsUnreadableInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, _, err := digestFile(root, "missing"); err == nil {
		t.Fatal("expected missing artifact to fail")
	}
	if _, _, err := digestFile(root, "."); err == nil {
		t.Fatal("expected directory artifact to fail")
	}
}

func writeTestArtifact(t *testing.T, directory, filename string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, filename), contents, 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func readTestFile(t *testing.T, directory, filename string) []byte {
	t.Helper()
	contents, err := fs.ReadFile(os.DirFS(directory), filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return contents
}

func verifyChecksumFile(t *testing.T, directory string) {
	t.Helper()
	contents := string(readTestFile(t, directory, checksumsFilename))
	for _, line := range strings.Split(strings.TrimSpace(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid checksum line %q", line)
		}
		digest, _, err := digestFile(directory, fields[1])
		if err != nil {
			t.Fatalf("digest %s: %v", fields[1], err)
		}
		if digest != fields[0] {
			t.Errorf("checksum for %s = %s, want %s", fields[1], digest, fields[0])
		}
	}
}

func TestWriteBuildMetadataRejectsOversizedProvenanceWithoutChangingOutputs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "oversized-lock.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((1 << 20) + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{publishedSourceLockFilename, releaseManifestFilename, checksumsFilename} {
		writeTestArtifact(t, root, name, []byte("last-known-good"))
	}
	err = writeBuildMetadata(root, path, sourceLock{}, strings.Repeat("a", 40), "go1.24.12")
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Errorf("oversized provenance error = %v", err)
	}
	for _, name := range []string{publishedSourceLockFilename, releaseManifestFilename, checksumsFilename} {
		if actual := string(readTestFile(t, root, name)); actual != "last-known-good" {
			t.Errorf("%s changed: %q", name, actual[:min(len(actual), 50)])
		}
	}
}

// Exercises verified downloads, actual converter adapters and provenance with
// synthetic fixed pins/epoch. It does not exercise the downstream real reader.
func TestSyntheticPinnedBundleRebuildIsByteIdentical(t *testing.T) {
	t.Parallel()
	siteBytes, err := proto.Marshal(&pb.GeoSiteList{Entry: []*pb.GeoSite{{CountryCode: "TEST", Domain: []*pb.Domain{{Type: pb.Domain_Domain, Value: "example.test"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	ipBytes, err := proto.Marshal(&pb.GeoIPList{Entry: []*pb.GeoIP{{CountryCode: "US", Cidr: []*pb.CIDR{{Ip: netip.MustParseAddr("192.0.2.0").AsSlice(), Prefix: 24}}}}})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := decodeSourceLock(strings.NewReader(validSourceLockJSON()))
	if err != nil {
		t.Fatal(err)
	}
	source := &lock.Sources[0]
	source.GeoSite.Size, source.GeoSite.SHA256 = int64(len(siteBytes)), testSHA256(siteBytes)
	source.GeoIP.Size, source.GeoIP.SHA256 = int64(len(ipBytes)), testSHA256(ipBytes)
	lockBytes, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	// Both decode and provenance reread must accept an exact-limit valid lock.
	lockBytes = append(lockBytes, bytes.Repeat([]byte(" "), (1<<20)-len(lockBytes))...)
	root := t.TempDir()
	lockPath := filepath.Join(root, "pinned.json")
	if err := os.WriteFile(lockPath, lockBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err = loadSourceLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	source = &lock.Sources[0]
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var contents []byte
		switch request.URL.String() {
		case source.GeoSite.URL:
			contents = siteBytes
		case source.GeoIP.URL:
			contents = ipBytes
		default:
			return nil, errors.New("unexpected fixture URL")
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(contents))}, nil
	})}
	outputs := []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
	for _, directory := range outputs {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		sitePath, ipPath := filepath.Join(directory, "geosite.dat"), filepath.Join(directory, "geoip.dat")
		if err := downloadVerifiedFileWithClient(client, source.GeoSite.URL, sitePath, source.GeoSite.SHA256, source.GeoSite.Size); err != nil {
			t.Fatal(err)
		}
		if err := downloadVerifiedFileWithClient(client, source.GeoIP.URL, ipPath, source.GeoIP.SHA256, source.GeoIP.Size); err != nil {
			t.Fatal(err)
		}
		if err := geosite.DatToJSON(sitePath, filepath.Join(directory, "official_geosite.json")); err != nil {
			t.Fatal(err)
		}
		if err := geoip.DatToMMDBWithOptions(ipPath, filepath.Join(directory, "official_geoip.mmdb"), geoip.MMDBOptions{BuildEpoch: source.GeoIP.BuildEpoch}); err != nil {
			t.Fatal(err)
		}
		if err := writeBuildMetadata(directory, lockPath, lock, strings.Repeat("c", 40), "go1.24.12"); err != nil {
			t.Fatal(err)
		}
		verifyChecksumFile(t, directory)
	}
	for _, name := range []string{"official_geosite.json", "official_geoip.mmdb", publishedSourceLockFilename, releaseManifestFilename, checksumsFilename} {
		first, second := readTestFile(t, outputs[0], name), readTestFile(t, outputs[1], name)
		if !bytes.Equal(first, second) {
			t.Errorf("same pins/epoch produced different %s", name)
		}
	}
}
