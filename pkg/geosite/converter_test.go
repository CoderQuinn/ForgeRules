package geosite

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/CoderQuinn/ForgeRules/proto"
	"google.golang.org/protobuf/proto"
)

func TestDatToJSONMatchesForgeRuleCoreGoldenFixture(t *testing.T) {
	t.Parallel()

	input := &pb.GeoSiteList{Entry: []*pb.GeoSite{
		{
			CountryCode: "category-test",
			Domain: []*pb.Domain{
				{Type: pb.Domain_Full, Value: "exact.example.test"},
				{Type: pb.Domain_Domain, Value: "example.test"},
				{Type: pb.Domain_Plain, Value: "keyword-token"},
			},
		},
	}}
	protobuf, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "geosite.dat")
	outputPath := filepath.Join(tempDir, "geosite.json")
	if err := os.WriteFile(inputPath, protobuf, 0o600); err != nil {
		t.Fatalf("write protobuf input: %v", err)
	}
	if err := DatToJSON(inputPath, outputPath); err != nil {
		t.Fatalf("convert input: %v", err)
	}

	actual, err := fs.ReadFile(os.DirFS(tempDir), "geosite.json")
	if err != nil {
		t.Fatalf("read converted output: %v", err)
	}
	expected, err := fs.ReadFile(os.DirFS("../../testdata/golden"), "geosite.json")
	if err != nil {
		t.Fatalf("read golden output: %v", err)
	}
	if !bytes.Equal(actual, expected) {
		t.Errorf("converted output does not match ForgeRuleCore golden fixture\nactual:\n%s\nexpected:\n%s", actual, expected)
	}
}

func TestDatToJSONSupportsEveryKnownDomainTypeAndAttribute(t *testing.T) {
	t.Parallel()

	input := &pb.GeoSiteList{Entry: []*pb.GeoSite{{
		CountryCode: "category-all-types",
		Domain: []*pb.Domain{
			{
				Type:  pb.Domain_Plain,
				Value: "keyword",
				Attribute: []*pb.Domain_Attribute{
					{Key: "enabled", TypedValue: &pb.Domain_Attribute_BoolValue{BoolValue: true}},
					{Key: "priority", TypedValue: &pb.Domain_Attribute_IntValue{IntValue: 7}},
				},
			},
			{Type: pb.Domain_Regex, Value: `^regex\\.example$`},
			{Type: pb.Domain_Domain, Value: "domain.example"},
			{Type: pb.Domain_Full, Value: "full.example"},
		},
	}}}
	tempDir := t.TempDir()
	inputPath := writeGeoSiteFixture(t, tempDir, input)
	outputPath := filepath.Join(tempDir, "geosite.json")
	if err := DatToJSON(inputPath, outputPath); err != nil {
		t.Fatalf("convert input: %v", err)
	}

	var output GeoSiteListJSON
	if err := json.Unmarshal(readGeoSiteFile(t, outputPath), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(output.GeoSites) != 1 || len(output.GeoSites[0].Domains) != 4 {
		t.Fatalf("output = %#v", output)
	}
	types := []string{"plain", "regex", "domain", "full"}
	for index, expectedType := range types {
		if output.GeoSites[0].Domains[index].Type != expectedType {
			t.Errorf("domain %d type = %q, want %q", index, output.GeoSites[0].Domains[index].Type, expectedType)
		}
	}
	attributes := output.GeoSites[0].Domains[0].Attributes
	if attributes["enabled"] != true || attributes["priority"] != float64(7) {
		t.Errorf("attributes = %#v", attributes)
	}
}

func TestDatToJSONRejectsUnknownDomainTypeWithoutReplacingOutput(t *testing.T) {
	t.Parallel()

	input := &pb.GeoSiteList{Entry: []*pb.GeoSite{{
		CountryCode: "category-unknown",
		Domain: []*pb.Domain{{
			Type:  pb.Domain_Type(99),
			Value: "silently-dropped.example",
		}},
	}}}
	tempDir := t.TempDir()
	inputPath := writeGeoSiteFixture(t, tempDir, input)
	outputPath := filepath.Join(tempDir, "geosite.json")
	const lastKnownGood = "last-known-good"
	if err := os.WriteFile(outputPath, []byte(lastKnownGood), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	err := DatToJSON(inputPath, outputPath)
	if err == nil {
		t.Fatal("expected unknown domain type to fail")
	}
	if !strings.Contains(err.Error(), "unsupported domain type 99") {
		t.Errorf("error = %v", err)
	}
	if actual := string(readGeoSiteFile(t, outputPath)); actual != lastKnownGood {
		t.Errorf("output = %q, want preserved %q", actual, lastKnownGood)
	}
	freshOutputPath := filepath.Join(tempDir, "fresh.json")
	if err := DatToJSON(inputPath, freshOutputPath); err == nil {
		t.Fatal("expected unknown domain type to fail for a fresh output")
	}
	if _, err := os.Stat(freshOutputPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unknown domain type published a fresh output, stat error = %v", err)
	}
}

func TestDatToJSONRejectsUnreadableMalformedAndUnwritablePaths(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "geosite.json")
	if err := DatToJSON(filepath.Join(tempDir, "missing.dat"), outputPath); err == nil {
		t.Fatal("expected missing input to fail")
	}
	malformedPath := filepath.Join(tempDir, "malformed.dat")
	if err := os.WriteFile(malformedPath, []byte{0xff}, 0o600); err != nil {
		t.Fatalf("write malformed input: %v", err)
	}
	if err := DatToJSON(malformedPath, outputPath); err == nil {
		t.Fatal("expected malformed protobuf to fail")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed conversions published an output, stat error = %v", err)
	}

	validPath := writeGeoSiteFixture(t, tempDir, &pb.GeoSiteList{})
	if err := DatToJSON(validPath, filepath.Join(tempDir, "missing", "geosite.json")); err == nil {
		t.Fatal("expected missing output directory to fail")
	}

	directoryTarget := filepath.Join(tempDir, "directory-target")
	if err := os.Mkdir(directoryTarget, 0o700); err != nil {
		t.Fatalf("create directory target: %v", err)
	}
	if err := writeJSONAtomically(directoryTarget, []byte("json")); err == nil {
		t.Fatal("expected replacement of a directory to fail")
	}
}

func writeGeoSiteFixture(t *testing.T, directory string, input *pb.GeoSiteList) string {
	t.Helper()
	data, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	path := filepath.Join(directory, "geosite.dat")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return path
}

func readGeoSiteFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test paths are created under t.TempDir.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
