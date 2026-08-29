package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const downloadTimeout = 2 * time.Minute

var downloadClient = &http.Client{Timeout: downloadTimeout}

func downloadVerifiedFile(url, path, expectedSHA256 string, expectedSize int64) error {
	return downloadVerifiedFileWithClient(
		downloadClient,
		url,
		path,
		expectedSHA256,
		expectedSize,
	)
}

func downloadVerifiedFileWithClient(
	client *http.Client,
	url, path, expectedSHA256 string,
	expectedSize int64,
) error {
	if expectedSize <= 0 {
		return fmt.Errorf("expected download size must be positive")
	}
	expectedDigest, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expectedDigest) != sha256.Size {
		return fmt.Errorf("expected SHA-256 must be 64 hexadecimal characters")
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary download target: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(temp, hash),
		io.LimitReader(resp.Body, expectedSize+1),
	)
	if err != nil {
		return fmt.Errorf("write temporary download target: %w", err)
	}
	if written != expectedSize {
		return fmt.Errorf("download size = %d, want %d", written, expectedSize)
	}
	actualSHA256 := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualSHA256, expectedSHA256) {
		return fmt.Errorf("download SHA-256 = %s, want %s", actualSHA256, expectedSHA256)
	}

	if err := temp.Chmod(0o644); err != nil {
		return fmt.Errorf("set download permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary download target: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace download target: %w", err)
	}
	keepTemp = true
	return nil
}
