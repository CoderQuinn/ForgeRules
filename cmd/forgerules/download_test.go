package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadVerifiedFileWithClient(t *testing.T) {
	t.Parallel()

	contents := []byte("rules")
	server := httptest.NewServer(binaryFixtureHandler(contents))
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

func TestDownloadVerifiedFileUsesProductionClient(t *testing.T) {
	t.Parallel()

	contents := []byte("rules")
	server := httptest.NewServer(binaryFixtureHandler(contents))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "rules.dat")
	if err := downloadVerifiedFile(
		server.URL,
		path,
		testSHA256(contents),
		int64(len(contents)),
	); err != nil {
		t.Fatalf("download file: %v", err)
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
	server := httptest.NewServer(binaryFixtureHandler(contents))
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

func TestDownloadVerifiedFileWithClientRejectsInvalidExpectations(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid expectations must fail before a request")
		return nil, nil
	})}
	path := filepath.Join(t.TempDir(), "rules.dat")
	if err := downloadVerifiedFileWithClient(client, "https://example.invalid", path, stringsOf("a", 64), 0); err == nil {
		t.Fatal("expected non-positive size to fail")
	}
	if err := downloadVerifiedFileWithClient(client, "https://example.invalid", path, "not-a-digest", 1); err == nil {
		t.Fatal("expected malformed digest to fail")
	}
}

func TestDownloadVerifiedFileWithClientRejectsUnexpectedSizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contents     []byte
		expectedSize int64
	}{
		{name: "short", contents: []byte("rule"), expectedSize: 5},
		{name: "long", contents: []byte("rules-extra"), expectedSize: 5},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(binaryFixtureHandler(test.contents))
			t.Cleanup(server.Close)
			path := filepath.Join(t.TempDir(), "rules.dat")
			err := downloadVerifiedFileWithClient(
				server.Client(),
				server.URL,
				path,
				testSHA256(test.contents),
				test.expectedSize,
			)
			if err == nil {
				t.Fatal("expected size mismatch to fail")
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("download target should not exist, stat error = %v", statErr)
			}
		})
	}
}

func TestDownloadVerifiedFileWithClientCleansUpReadAndTargetErrors(t *testing.T) {
	t.Parallel()

	readFailureClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(errorReader{}),
		}, nil
	})}
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "rules.dat")
	if err := downloadVerifiedFileWithClient(
		readFailureClient,
		"https://example.invalid/rules.dat",
		path,
		testSHA256([]byte("rules")),
		5,
	); err == nil {
		t.Fatal("expected response read failure")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("read failure published target, stat error = %v", err)
	}

	contents := []byte("rules")
	server := httptest.NewServer(binaryFixtureHandler(contents))
	t.Cleanup(server.Close)
	missingParent := filepath.Join(tempDir, "missing", "rules.dat")
	if err := downloadVerifiedFileWithClient(
		server.Client(),
		server.URL,
		missingParent,
		testSHA256(contents),
		int64(len(contents)),
	); err == nil {
		t.Fatal("expected missing target directory to fail")
	}

	directoryTarget := filepath.Join(tempDir, "directory-target")
	if err := os.Mkdir(directoryTarget, 0o700); err != nil {
		t.Fatalf("create directory target: %v", err)
	}
	if err := downloadVerifiedFileWithClient(
		server.Client(),
		server.URL,
		directoryTarget,
		testSHA256(contents),
		int64(len(contents)),
	); err == nil {
		t.Fatal("expected replacement of a directory to fail")
	}
	if info, err := os.Stat(directoryTarget); err != nil || !info.IsDir() {
		t.Errorf("directory target was not preserved, info = %v, error = %v", info, err)
	}
}

func testSHA256(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func binaryFixtureHandler(contents []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.ServeContent(w, request, "rules.dat", time.Unix(0, 0), bytes.NewReader(contents))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("injected response read failure")
}

func stringsOf(character string, count int) string {
	return strings.Repeat(character, count)
}
