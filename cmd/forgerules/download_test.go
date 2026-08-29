package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadVerifiedFileWithClient(t *testing.T) {
	t.Parallel()

	contents := []byte("rules")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(contents)
	}))
	t.Cleanup(server.Close)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "rules.dat")
	if err := downloadVerifiedFileWithClient(
		server.Client(), server.URL, path, testSHA256(contents), int64(len(contents)),
	); err != nil {
		t.Fatalf("download file: %v", err)
	}

	data, err := fs.ReadFile(os.DirFS(tempDir), "rules.dat")
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != string(contents) {
		t.Errorf("downloaded data = %q, want %q", data, contents)
	}
}

func TestDownloadVerifiedFileWithClientRejectsNonOKResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "rules.dat")
	err := downloadVerifiedFileWithClient(
		server.Client(), server.URL, path, testSHA256([]byte("rules")), 5,
	)
	if err == nil {
		t.Fatal("expected non-OK response to fail")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("download target should not exist, stat error = %v", statErr)
	}
}

func TestDownloadVerifiedFileWithClientTimesOut(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "rules.dat")

	start := time.Now()
	err := downloadVerifiedFileWithClient(
		client, server.URL, path, testSHA256([]byte("rules")), 5,
	)
	if err == nil {
		t.Fatal("expected timed-out request to fail")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("request took %s despite client timeout", elapsed)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("download target should not exist, stat error = %v", statErr)
	}
}

func TestDownloadVerifiedFileWithClientPreservesTargetOnDigestMismatch(t *testing.T) {
	t.Parallel()

	contents := []byte("rules")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(contents)
	}))
	t.Cleanup(server.Close)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "rules.dat")
	if err := os.WriteFile(path, []byte("last-known-good"), 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	err := downloadVerifiedFileWithClient(
		server.Client(), server.URL, path, testSHA256([]byte("other")), int64(len(contents)),
	)
	if err == nil {
		t.Fatal("expected digest mismatch to fail")
	}

	data, readErr := fs.ReadFile(os.DirFS(tempDir), "rules.dat")
	if readErr != nil {
		t.Fatalf("read preserved target: %v", readErr)
	}
	if string(data) != "last-known-good" {
		t.Errorf("preserved target = %q, want %q", data, "last-known-good")
	}
}

func testSHA256(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
