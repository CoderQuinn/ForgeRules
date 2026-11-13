package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		"empty sources": `{"schemaVersion": 1, "sources": []}`,
		"unknown field": strings.Replace(
			validSourceLockJSON(),
			`"schemaVersion": 1`,
			`"schemaVersion": 1, "extra": true`,
			1,
		),
		"unknown schema": strings.Replace(
			validSourceLockJSON(), `"schemaVersion": 1`, `"schemaVersion": 2`, 1,
		),
		"invalid source name": strings.Replace(
			validSourceLockJSON(), `"name": "official"`, `"name": "bad/name"`, 1,
		),
		"empty release tag": strings.Replace(
			validSourceLockJSON(), `"releaseTag": "v1"`, `"releaseTag": ""`, 1,
		),
		"invalid repository shape": strings.Replace(
			validSourceLockJSON(),
			`"repository": "v2fly/domain-list-community"`,
			`"repository": "domain-list-community"`,
			1,
		),
		"invalid revision": strings.Replace(
			validSourceLockJSON(), strings.Repeat("1", 40), "not-a-revision", 1,
		),
		"invalid publication time": strings.Replace(
			validSourceLockJSON(), "2023-11-14T22:13:20Z", "not-a-time", 1,
		),
		"non-positive size": strings.Replace(
			validSourceLockJSON(), `"size": 5`, `"size": 0`, 1,
		),
		"uppercase digest": strings.Replace(
			validSourceLockJSON(), strings.Repeat("a", 64), strings.Repeat("A", 64), 1,
		),
		"invalid URL encoding": strings.Replace(
			validSourceLockJSON(),
			"https://github.com/v2fly/domain-list-community/releases/download/v1/dlc.dat",
			"https://github.com/%zz",
			1,
		),
		"non-HTTPS URL": strings.Replace(
			validSourceLockJSON(), "https://github.com/", "http://github.com/", 1,
		),
		"wrong URL host": strings.Replace(
			validSourceLockJSON(), "https://github.com/", "https://example.com/", 1,
		),
		"mutable latest URL": strings.ReplaceAll(
			validSourceLockJSON(), "/download/v1/", "/download/latest/",
		),
		"unapproved repository": strings.Replace(
			validSourceLockJSON(),
			"v2fly/domain-list-community",
			"example/private-rules",
			1,
		),
		"URL credentials": strings.Replace(
			validSourceLockJSON(),
			"https://github.com/v2fly/domain-list-community/",
			"https://redacted@github.com/v2fly/domain-list-community/",
			1,
		),
		"URL query parameters": strings.Replace(
			validSourceLockJSON(),
			"/dlc.dat\"",
			"/dlc.dat?token=redacted\"",
			1,
		),
		"URL fragment": strings.Replace(
			validSourceLockJSON(),
			"/dlc.dat\"",
			"/dlc.dat#private\"",
			1,
		),
		"asset not approved for repository": strings.Replace(
			validSourceLockJSON(),
			"/dlc.dat\"",
			"/geosite.dat\"",
			1,
		),
		"invalid digest": strings.Replace(
			validSourceLockJSON(), strings.Repeat("a", 64), "abc", 1,
		),
		"mismatched epoch": strings.Replace(
			validSourceLockJSON(), `"buildEpoch": 1700000000`, `"buildEpoch": 1700000001`, 1,
		),
		"non-positive build epoch": strings.Replace(
			validSourceLockJSON(), `"buildEpoch": 1700000000`, `"buildEpoch": 0`, 1,
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

func TestDecodeSourceLockRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	if _, err := decodeSourceLock(strings.NewReader(validSourceLockJSON() + `{}`)); err == nil {
		t.Fatal("expected trailing JSON value to fail")
	}
	if _, err := decodeSourceLock(strings.NewReader(`{"schemaVersion":`)); err == nil {
		t.Fatal("expected malformed JSON to fail")
	}
}

func TestDecodeSourceLockRejectsInvalidGeoIPContract(t *testing.T) {
	t.Parallel()

	contents := strings.Replace(
		validSourceLockJSON(),
		strings.Repeat("b", 64),
		"not-a-digest",
		1,
	)
	_, err := decodeSourceLock(strings.NewReader(contents))
	if err == nil {
		t.Fatal("expected invalid geoip contract to fail")
	}
	if !strings.Contains(err.Error(), `source "official" geoip sha256`) {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadSourceLock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "rules.sources.lock.json")
	if err := os.WriteFile(path, []byte(validSourceLockJSON()), 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}
	lock, err := loadSourceLock(path)
	if err != nil {
		t.Fatalf("load source lock: %v", err)
	}
	if len(lock.Sources) != 1 || lock.Sources[0].Name != "official" {
		t.Errorf("lock = %#v", lock)
	}
	if _, err := loadSourceLock(filepath.Join(root, "missing.json")); err == nil {
		t.Fatal("expected missing source lock to fail")
	}
	malformedPath := filepath.Join(root, "malformed.json")
	if err := os.WriteFile(malformedPath, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed source lock: %v", err)
	}
	if _, err := loadSourceLock(malformedPath); err == nil {
		t.Fatal("expected malformed source lock to fail")
	}
}

func TestLockedAssetRejectsUnsupportedNameForContract(t *testing.T) {
	t.Parallel()

	asset := lockedAsset{
		Repository:  "v2fly/domain-list-community",
		ReleaseTag:  "v1",
		Revision:    strings.Repeat("1", 40),
		PublishedAt: "2023-11-14T22:13:20Z",
		URL:         "https://github.com/v2fly/domain-list-community/releases/download/v1/dlc.dat",
		SHA256:      strings.Repeat("a", 64),
		Size:        5,
	}
	if err := asset.validate("geosite", "official", "different.dat"); err == nil {
		t.Fatal("expected contract-specific asset name mismatch to fail")
	}
}

func validSourceLockJSON() string {
	return fmt.Sprintf(`{"schemaVersion": 1, "sources": [%s]}`, validLockedSourceJSON())
}

func validLockedSourceJSON() string {
	return fmt.Sprintf(`{
		"name": "official",
		"geosite": {
			"repository": "v2fly/domain-list-community",
			"releaseTag": "v1",
			"revision": "%s",
			"publishedAt": "2023-11-14T22:13:20Z",
			"url": "https://github.com/v2fly/domain-list-community/releases/download/v1/dlc.dat",
			"sha256": "%s",
			"size": 5
		},
		"geoip": {
			"repository": "v2fly/geoip",
			"releaseTag": "v1",
			"revision": "%s",
			"publishedAt": "2023-11-14T22:13:20Z",
			"url": "https://github.com/v2fly/geoip/releases/download/v1/geoip.dat",
			"sha256": "%s",
			"size": 5,
			"buildEpoch": 1700000000
		}
	}`, strings.Repeat("1", 40), strings.Repeat("a", 64), strings.Repeat("2", 40), strings.Repeat("b", 64))
}

// Catches a decoder that accepts oversized whitespace, including streamed input.
func TestDecodeSourceLockEnforcesByteBudget(t *testing.T) {
	t.Parallel()
	const limit = 1 << 20
	valid := validSourceLockJSON()
	exact := valid + strings.Repeat(" ", limit-len(valid))
	if _, err := decodeSourceLock(strings.NewReader(exact)); err != nil {
		t.Fatalf("exact-limit lock rejected: %v", err)
	}
	if _, err := decodeSourceLock(strings.NewReader(exact + " ")); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized lock error = %v", err)
	}
	if _, err := decodeSourceLock(errorReader{}); err == nil || !strings.Contains(err.Error(), "injected response read failure") {
		t.Fatalf("lock read failure = %v", err)
	}
}

// Catches size declarations that could reach download size+1 arithmetic before
// the kind-specific absolute budget is checked.
func TestSourceLockRejectsOversizedDeclarations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field string
		size        int64
	}{
		{"geosite", `"size": 5`, (32 << 20) + 1},
		{"geoip", `"size": 5,`, (64 << 20) + 1},
		{"overflow", `"size": 5,`, 9223372036854775807},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := strings.Replace(validSourceLockJSON(), test.field, fmt.Sprintf(`"size": %d`, test.size)+strings.TrimPrefix(test.field, `"size": 5`), 1)
			if _, err := decodeSourceLock(strings.NewReader(contents)); err == nil || !strings.Contains(err.Error(), "byte limit") {
				t.Fatalf("oversized declaration error = %v", err)
			}
		})
	}
}

func TestLoadSourceLockRejectsOversizedSparseInput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "oversized.json")
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
	if _, err := loadSourceLock(path); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized lock error = %v", err)
	}
}

func TestSourceLockAcceptsExactDeclarationBudgets(t *testing.T) {
	t.Parallel()
	contents := strings.Replace(validSourceLockJSON(), `"size": 5`, `"size": 33554432`, 1)
	contents = strings.Replace(contents, `"size": 5`, `"size": 67108864`, 1)
	if _, err := decodeSourceLock(strings.NewReader(contents)); err != nil {
		t.Fatalf("exact declaration budgets rejected: %v", err)
	}
}
