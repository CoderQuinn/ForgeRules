package geosite

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
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
