package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const downloadTimeout = 2 * time.Minute

var downloadClient = &http.Client{Timeout: downloadTimeout}

func downloadFile(url, path string) error {
	return downloadFileWithClient(downloadClient, url, path)
}

func downloadFileWithClient(client *http.Client, url, path string) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create download target: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write download target: %w", err)
	}

	return nil
}
