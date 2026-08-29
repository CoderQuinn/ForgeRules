package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	releaseManifestSchemaVersion = 1
	releaseManifestFilename      = "rules-manifest.json"
	checksumsFilename            = "SHA256SUMS"
	publishedSourceLockFilename  = "rules.sources.lock.json"
	geoSiteFormat                = "forgerules.geosite.v1"
	geoIPFormat                  = "maxminddb.GeoIP2-Country.v1"
)

type releaseManifest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Converter     converterProvenance `json:"converter"`
	SourceLock    sourceLockProvenance `json:"sourceLock"`
	Bundles       []bundleProvenance  `json:"bundles"`
}

type converterProvenance struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	GoVersion  string `json:"goVersion"`
}

type sourceLockProvenance struct {
	File    string         `json:"file"`
	SHA256  string         `json:"sha256"`
	Sources []lockedSource `json:"sources"`
}

type bundleProvenance struct {
	Name    string             `json:"name"`
	GeoSite artifactProvenance `json:"geosite"`
	GeoIP   artifactProvenance `json:"geoip"`
}

type artifactProvenance struct {
	File       string      `json:"file"`
	Format     string      `json:"format"`
	Size       int64       `json:"size"`
	SHA256     string      `json:"sha256"`
	BuildEpoch int64       `json:"buildEpoch,omitempty"`
	Source     lockedAsset `json:"source"`
}

type checksumEntry struct {
	Filename string
	SHA256   string
}

func writeBuildMetadata(
	outputDirectory, sourceLockPath string,
	lock sourceLock,
	converterRevision, goVersion string,
) error {
	if !revisionPattern.MatchString(converterRevision) {
		return fmt.Errorf("converter revision is not a lowercase 40-character commit SHA")
	}
	if strings.TrimSpace(goVersion) == "" {
		return fmt.Errorf("Go version is empty")
	}

	lockFile, err := os.DirFS(filepath.Dir(sourceLockPath)).Open(filepath.Base(sourceLockPath))
	if err != nil {
		return fmt.Errorf("open source lock for provenance: %w", err)
	}
	lockContents, err := io.ReadAll(lockFile)
	closeErr := lockFile.Close()
	if err != nil {
		return fmt.Errorf("read source lock for provenance: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close source lock after provenance read: %w", closeErr)
	}
	lockSHA256 := sha256Hex(lockContents)
	publishedLockPath := filepath.Join(outputDirectory, publishedSourceLockFilename)
	if err := writeFileAtomically(publishedLockPath, lockContents, 0o644); err != nil {
		return fmt.Errorf("publish source lock: %w", err)
	}

	manifest := releaseManifest{
		SchemaVersion: releaseManifestSchemaVersion,
		Converter: converterProvenance{
			Repository: "CoderQuinn/ForgeRules",
			Revision:   converterRevision,
			GoVersion:  goVersion,
		},
		SourceLock: sourceLockProvenance{
			File:    publishedSourceLockFilename,
			SHA256:  lockSHA256,
			Sources: lock.Sources,
		},
		Bundles: make([]bundleProvenance, 0, len(lock.Sources)),
	}
	checksums := []checksumEntry{
		{Filename: publishedSourceLockFilename, SHA256: lockSHA256},
	}

	for _, source := range lock.Sources {
		geoSite, err := artifactMetadata(
			outputDirectory,
			source.Name+"_geosite.json",
			geoSiteFormat,
			0,
			source.GeoSite,
		)
		if err != nil {
			return err
		}
		geoIP, err := artifactMetadata(
			outputDirectory,
			source.Name+"_geoip.mmdb",
			geoIPFormat,
			source.GeoIP.BuildEpoch,
			source.GeoIP.lockedAsset,
		)
		if err != nil {
			return err
		}
		manifest.Bundles = append(manifest.Bundles, bundleProvenance{
			Name:    source.Name,
			GeoSite: geoSite,
			GeoIP:   geoIP,
		})
		checksums = append(
			checksums,
			checksumEntry{Filename: geoSite.File, SHA256: geoSite.SHA256},
			checksumEntry{Filename: geoIP.File, SHA256: geoIP.SHA256},
		)
	}

	manifestContents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release manifest: %w", err)
	}
	manifestContents = append(manifestContents, '\n')
	manifestPath := filepath.Join(outputDirectory, releaseManifestFilename)
	if err := writeFileAtomically(manifestPath, manifestContents, 0o644); err != nil {
		return fmt.Errorf("write release manifest: %w", err)
	}
	checksums = append(checksums, checksumEntry{
		Filename: releaseManifestFilename,
		SHA256:   sha256Hex(manifestContents),
	})

	sort.Slice(checksums, func(i, j int) bool {
		return checksums[i].Filename < checksums[j].Filename
	})
	var checksumContents strings.Builder
	for _, entry := range checksums {
		fmt.Fprintf(&checksumContents, "%s  %s\n", entry.SHA256, entry.Filename)
	}
	if err := writeFileAtomically(
		filepath.Join(outputDirectory, checksumsFilename),
		[]byte(checksumContents.String()),
		0o644,
	); err != nil {
		return fmt.Errorf("write checksums: %w", err)
	}
	return nil
}

func artifactMetadata(
	outputDirectory, filename, format string,
	buildEpoch int64,
	source lockedAsset,
) (artifactProvenance, error) {
	digest, size, err := digestFile(outputDirectory, filename)
	if err != nil {
		return artifactProvenance{}, fmt.Errorf("inspect artifact %s: %w", filename, err)
	}
	return artifactProvenance{
		File:       filename,
		Format:     format,
		Size:       size,
		SHA256:     digest,
		BuildEpoch: buildEpoch,
		Source:     source,
	}, nil
}

func digestFile(directory, filename string) (string, int64, error) {
	file, err := os.DirFS(directory).Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func writeFileAtomically(filePath string, contents []byte, permissions os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(filePath), "."+filepath.Base(filePath)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(contents); err != nil {
		return err
	}
	if err := temp.Chmod(permissions); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, filePath); err != nil {
		return err
	}
	keepTemp = true
	return nil
}
