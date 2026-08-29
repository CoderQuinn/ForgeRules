package main

import (
	"bytes"
	"encoding/json"
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
