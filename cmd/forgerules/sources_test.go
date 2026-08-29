package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeSourceLock(t *testing.T) {
	t.Parallel()

	lock, err := decodeSourceLock(strings.NewReader(validSourceLockJSON()))
	if err != nil {
		t.Fatalf("decode source lock: %v", err)
	}
	if len(lock.Sources) != 1 || lock.Sources[0].Name != "official" {
		t.Fatalf("decoded sources = %#v", lock.Sources)
	}
}

func TestDecodeSourceLockRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown field": strings.Replace(
			validSourceLockJSON(),
			`"schemaVersion": 1`,
			`"schemaVersion": 1, "extra": true`,
			1,
		),
		"unknown schema": strings.Replace(
			validSourceLockJSON(), `"schemaVersion": 1`, `"schemaVersion": 2`, 1,
		),
		"mutable latest URL": strings.ReplaceAll(
			validSourceLockJSON(), "/download/v1/", "/download/latest/",
		),
		"invalid digest": strings.Replace(
			validSourceLockJSON(), strings.Repeat("a", 64), "abc", 1,
		),
		"mismatched epoch": strings.Replace(
			validSourceLockJSON(), `"buildEpoch": 1700000000`, `"buildEpoch": 1700000001`, 1,
		),
		"duplicate source name": strings.Replace(
			validSourceLockJSON(), `]}`, `,`+validLockedSourceJSON()+`]}`, 1,
		),
	}

	for name, contents := range tests {
		name, contents := name, contents
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeSourceLock(strings.NewReader(contents)); err == nil {
				t.Fatal("expected invalid source lock to fail")
			}
		})
	}
}

func validSourceLockJSON() string {
	return fmt.Sprintf(`{"schemaVersion": 1, "sources": [%s]}`, validLockedSourceJSON())
}

func validLockedSourceJSON() string {
	return fmt.Sprintf(`{
		"name": "official",
		"geosite": {
			"repository": "example/rules",
			"releaseTag": "v1",
			"revision": "%s",
			"publishedAt": "2023-11-14T22:13:20Z",
			"url": "https://github.com/example/rules/releases/download/v1/geosite.dat",
			"sha256": "%s",
			"size": 5
		},
		"geoip": {
			"repository": "example/rules",
			"releaseTag": "v1",
			"revision": "%s",
			"publishedAt": "2023-11-14T22:13:20Z",
			"url": "https://github.com/example/rules/releases/download/v1/geoip.dat",
			"sha256": "%s",
			"size": 5,
			"buildEpoch": 1700000000
		}
	}`, strings.Repeat("1", 40), strings.Repeat("a", 64), strings.Repeat("2", 40), strings.Repeat("b", 64))
}
