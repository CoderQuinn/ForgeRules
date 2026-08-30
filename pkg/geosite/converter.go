package geosite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	pb "github.com/CoderQuinn/ForgeRules/proto"
	"google.golang.org/protobuf/proto"
)

// DomainJSON represents a domain entry in JSON format
type DomainJSON struct {
	Type       string                 `json:"type"`
	Value      string                 `json:"value"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// GeoSiteJSON represents a geosite entry in JSON format
type GeoSiteJSON struct {
	CountryCode string       `json:"country_code"`
	Domains     []DomainJSON `json:"domains"`
}

// GeoSiteListJSON represents the complete geosite list in JSON format
type GeoSiteListJSON struct {
	GeoSites []GeoSiteJSON `json:"geosites"`
}

// DatToJSON converts a geosite.dat file to geosite.json
func DatToJSON(datPath, jsonPath string) error {
	// Read the .dat file
	data, err := os.ReadFile(datPath)
	if err != nil {
		return fmt.Errorf("failed to read dat file: %w", err)
	}

	// Unmarshal the protobuf data
	var geoSiteList pb.GeoSiteList
	if err := proto.Unmarshal(data, &geoSiteList); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}

	// Convert to JSON structure
	jsonList := GeoSiteListJSON{
		GeoSites: make([]GeoSiteJSON, 0, len(geoSiteList.Entry)),
	}

	for entryIndex, entry := range geoSiteList.Entry {
		if entry == nil {
			return fmt.Errorf("geosite entry %d is nil", entryIndex)
		}
		geoSite := GeoSiteJSON{
			CountryCode: entry.CountryCode,
			Domains:     make([]DomainJSON, 0, len(entry.Domain)),
		}

		for domainIndex, domain := range entry.Domain {
			if domain == nil {
				return fmt.Errorf("geosite entry %d (%q) domain %d is nil", entryIndex, entry.CountryCode, domainIndex)
			}
			domainType, err := domainTypeToString(domain.Type)
			if err != nil {
				return fmt.Errorf(
					"geosite entry %d (%q) domain %d (%q): %w",
					entryIndex,
					entry.CountryCode,
					domainIndex,
					domain.Value,
					err,
				)
			}
			domainJSON := DomainJSON{
				Type:  domainType,
				Value: domain.Value,
			}

			if len(domain.Attribute) > 0 {
				domainJSON.Attributes = make(map[string]interface{})
				for _, attr := range domain.Attribute {
					if boolVal, ok := attr.TypedValue.(*pb.Domain_Attribute_BoolValue); ok {
						domainJSON.Attributes[attr.Key] = boolVal.BoolValue
					} else if intVal, ok := attr.TypedValue.(*pb.Domain_Attribute_IntValue); ok {
						domainJSON.Attributes[attr.Key] = intVal.IntValue
					}
				}
			}

			geoSite.Domains = append(geoSite.Domains, domainJSON)
		}

		jsonList.GeoSites = append(jsonList.GeoSites, geoSite)
	}

	// Marshal to JSON with indentation
	jsonData, err := json.MarshalIndent(jsonList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	jsonData = append(jsonData, '\n')

	if err := writeJSONAtomically(jsonPath, jsonData); err != nil {
		return fmt.Errorf("failed to write JSON file: %w", err)
	}

	return nil
}

func domainTypeToString(t pb.Domain_Type) (string, error) {
	switch t {
	case pb.Domain_Plain:
		return "plain", nil
	case pb.Domain_Regex:
		return "regex", nil
	case pb.Domain_Domain:
		return "domain", nil
	case pb.Domain_Full:
		return "full", nil
	default:
		return "", fmt.Errorf("unsupported domain type %d", t)
	}
}

func writeJSONAtomically(jsonPath string, contents []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(jsonPath), "."+filepath.Base(jsonPath)+".*")
	if err != nil {
		return fmt.Errorf("create temporary JSON file: %w", err)
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
		return fmt.Errorf("write temporary JSON file: %w", err)
	}
	if err := temp.Chmod(0o644); err != nil {
		return fmt.Errorf("set temporary JSON permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary JSON file: %w", err)
	}
	if err := os.Rename(tempPath, jsonPath); err != nil {
		return fmt.Errorf("replace JSON output: %w", err)
	}

	keepTemp = true
	return nil
}
