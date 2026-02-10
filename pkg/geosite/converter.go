package geosite

import (
	"encoding/json"
	"fmt"
	"os"

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

	for _, entry := range geoSiteList.Entry {
		geoSite := GeoSiteJSON{
			CountryCode: entry.CountryCode,
			Domains:     make([]DomainJSON, 0, len(entry.Domain)),
		}

		for _, domain := range entry.Domain {
			domainJSON := DomainJSON{
				Type:  domainTypeToString(domain.Type),
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

	// Write to file
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON file: %w", err)
	}

	return nil
}

func domainTypeToString(t pb.Domain_Type) string {
	switch t {
	case pb.Domain_Plain:
		return "plain"
	case pb.Domain_Regex:
		return "regex"
	case pb.Domain_Domain:
		return "domain"
	case pb.Domain_Full:
		return "full"
	default:
		return "unknown"
	}
}
