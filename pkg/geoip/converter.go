package geoip

import (
	"fmt"
	"net"
	"os"
	"strings"

	pb "github.com/CoderQuinn/ForgeRules/proto"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"google.golang.org/protobuf/proto"
)

// DatToMMDB converts a geoip.dat file to MMDB format
func DatToMMDB(datPath, mmdbPath string) error {
	// Read the .dat file
	data, err := os.ReadFile(datPath)
	if err != nil {
		return fmt.Errorf("failed to read dat file: %w", err)
	}

	// Unmarshal the protobuf data
	var geoIPList pb.GeoIPList
	if err := proto.Unmarshal(data, &geoIPList); err != nil {
		return fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}

	// Create a new MMDB writer
	writer, err := mmdbwriter.New(
		mmdbwriter.Options{
			DatabaseType: "GeoIP2-Country",
			Description: map[string]string{
				"en": "GeoIP database converted from geoip.dat",
			},
			RecordSize: 28,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create MMDB writer: %w", err)
	}

	// Process each GeoIP entry
	for _, entry := range geoIPList.Entry {
		countryCode := entry.CountryCode

		// Create the record to insert
		record := mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(countryCode),
			},
		}

		// Insert each CIDR block
		for _, cidr := range entry.Cidr {
			ipNet, err := parseCIDR(cidr)
			if err != nil {
				fmt.Printf("Warning: skipping invalid CIDR: %v\n", err)
				continue
			}

			if err := writer.Insert(ipNet, record); err != nil {
				if strings.Contains(err.Error(), "reserved network") {
					continue
				}
				fmt.Printf("Warning: failed to insert CIDR %s: %v\n", ipNet.String(), err)
			}
		}
	}

	// Write the MMDB file
	file, err := os.Create(mmdbPath)
	if err != nil {
		return fmt.Errorf("failed to create MMDB file: %w", err)
	}
	defer file.Close()

	if _, err := writer.WriteTo(file); err != nil {
		return fmt.Errorf("failed to write MMDB data: %w", err)
	}

	return nil
}

// parseCIDR converts a pb.CIDR to a net.IPNet
func parseCIDR(cidr *pb.CIDR) (*net.IPNet, error) {
	ip := net.IP(cidr.Ip)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address")
	}

	// Determine IP version
	if ip.To4() != nil {
		// IPv4
		ip = ip.To4()
	}

	// Create the CIDR string
	cidrStr := fmt.Sprintf("%s/%d", ip.String(), cidr.Prefix)
	_, ipNet, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CIDR %s: %w", cidrStr, err)
	}

	return ipNet, nil
}
