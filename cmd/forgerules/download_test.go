package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadFileWithClient(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("rules"))
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "rules.dat")
	if err := downloadFileWithClient(server.Client(), server.URL, path); err != nil {
		t.Fatalf("download file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "rules" {
		t.Errorf("downloaded data = %q, want %q", data, "rules")
	}
}

func TestDownloadFileWithClientRejectsNonOKResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), "rules.dat")
	err := downloadFileWithClient(server.Client(), server.URL, path)
	if err == nil {
		t.Fatal("expected non-OK response to fail")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("download target should not exist, stat error = %v", statErr)
	}
}

func TestDownloadFileWithClientTimesOut(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "rules.dat")

	start := time.Now()
	err := downloadFileWithClient(client, server.URL, path)
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
