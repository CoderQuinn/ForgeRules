package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
