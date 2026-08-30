package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

const sourceLockSchemaVersion = 1

var sourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var approvedPublicAssets = map[string]map[string]struct{}{
	"v2fly/domain-list-community": {
		"dlc.dat": {},
	},
	"v2fly/geoip": {
		"geoip.dat": {},
	},
	"Loyalsoldier/v2ray-rules-dat": {
		"geosite.dat": {},
		"geoip.dat":   {},
	},
}

type sourceLock struct {
	SchemaVersion int            `json:"schemaVersion"`
	Sources       []lockedSource `json:"sources"`
}

type lockedSource struct {
	Name    string           `json:"name"`
	GeoSite lockedAsset      `json:"geosite"`
	GeoIP   lockedGeoIPAsset `json:"geoip"`
}

type lockedAsset struct {
	Repository  string `json:"repository"`
	ReleaseTag  string `json:"releaseTag"`
	Revision    string `json:"revision"`
	PublishedAt string `json:"publishedAt"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

type lockedGeoIPAsset struct {
	lockedAsset
	BuildEpoch int64 `json:"buildEpoch"`
}

func loadSourceLock(filePath string) (sourceLock, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return sourceLock{}, fmt.Errorf("open source lock: %w", err)
	}
	defer file.Close()

	lock, err := decodeSourceLock(file)
	if err != nil {
		return sourceLock{}, fmt.Errorf("decode source lock: %w", err)
	}
	return lock, nil
}

func decodeSourceLock(reader io.Reader) (sourceLock, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var lock sourceLock
	if err := decoder.Decode(&lock); err != nil {
		return sourceLock{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return sourceLock{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return sourceLock{}, err
	}
	if err := lock.validate(); err != nil {
		return sourceLock{}, err
	}
	return lock, nil
}

func (lock sourceLock) validate() error {
	if lock.SchemaVersion != sourceLockSchemaVersion {
		return fmt.Errorf("schemaVersion = %d, want %d", lock.SchemaVersion, sourceLockSchemaVersion)
	}
	if len(lock.Sources) == 0 {
		return fmt.Errorf("sources must not be empty")
	}

	names := make(map[string]struct{}, len(lock.Sources))
	for index, source := range lock.Sources {
		if !sourceNamePattern.MatchString(source.Name) {
			return fmt.Errorf("sources[%d].name is invalid", index)
		}
		if _, exists := names[source.Name]; exists {
			return fmt.Errorf("source name %q is duplicated", source.Name)
		}
		names[source.Name] = struct{}{}

		if err := source.GeoSite.validate("geosite", source.Name, "dlc.dat", "geosite.dat"); err != nil {
			return err
		}
		if err := source.GeoIP.lockedAsset.validate("geoip", source.Name, "geoip.dat"); err != nil {
			return err
		}
		publishedAt, err := time.Parse(time.RFC3339, source.GeoIP.PublishedAt)
		if err != nil {
			return fmt.Errorf("source %q geoip publishedAt: %w", source.Name, err)
		}
		if source.GeoIP.BuildEpoch <= 0 {
			return fmt.Errorf("source %q geoip buildEpoch must be positive", source.Name)
		}
		if source.GeoIP.BuildEpoch != publishedAt.Unix() {
			return fmt.Errorf(
				"source %q geoip buildEpoch = %d, want publishedAt epoch %d",
				source.Name,
				source.GeoIP.BuildEpoch,
				publishedAt.Unix(),
			)
		}
	}
	return nil
}

func (asset lockedAsset) validate(kind, sourceName string, allowedNames ...string) error {
	label := fmt.Sprintf("source %q %s", sourceName, kind)
	if strings.Count(asset.Repository, "/") != 1 {
		return fmt.Errorf("%s repository is invalid", label)
	}
	publicAssets, approvedRepository := approvedPublicAssets[asset.Repository]
	if !approvedRepository {
		return fmt.Errorf("%s repository is not an approved public source", label)
	}
	if asset.ReleaseTag == "" {
		return fmt.Errorf("%s releaseTag is empty", label)
	}
	if !revisionPattern.MatchString(asset.Revision) {
		return fmt.Errorf("%s revision is not a lowercase 40-character commit SHA", label)
	}
	if _, err := time.Parse(time.RFC3339, asset.PublishedAt); err != nil {
		return fmt.Errorf("%s publishedAt: %w", label, err)
	}
	if asset.Size <= 0 {
		return fmt.Errorf("%s size must be positive", label)
	}
	digest, err := hex.DecodeString(asset.SHA256)
	if err != nil || len(digest) != 32 || asset.SHA256 != strings.ToLower(asset.SHA256) {
		return fmt.Errorf("%s sha256 must be 64 lowercase hexadecimal characters", label)
	}

	parsedURL, err := url.Parse(asset.URL)
	if err != nil {
		return fmt.Errorf("%s url: %w", label, err)
	}
	if parsedURL.Scheme != "https" || parsedURL.Host != "github.com" {
		return fmt.Errorf("%s url must use https://github.com", label)
	}
	if parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.ForceQuery || parsedURL.Fragment != "" {
		return fmt.Errorf("%s url must not include credentials, query parameters, or fragments", label)
	}
	expectedPrefix := "/" + asset.Repository + "/releases/download/" + asset.ReleaseTag + "/"
	if !strings.HasPrefix(parsedURL.Path, expectedPrefix) {
		return fmt.Errorf("%s url does not match repository and releaseTag", label)
	}
	assetName := path.Base(parsedURL.Path)
	if _, approvedAsset := publicAssets[assetName]; !approvedAsset {
		return fmt.Errorf("%s asset %q is not approved for repository %q", label, assetName, asset.Repository)
	}
	for _, allowedName := range allowedNames {
		if assetName == allowedName {
			return nil
		}
	}
	return fmt.Errorf("%s asset name %q is not supported", label, assetName)
}
