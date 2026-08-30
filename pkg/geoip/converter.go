package geoip

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	pb "github.com/CoderQuinn/ForgeRules/proto"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"google.golang.org/protobuf/proto"
)

// MMDBOptions controls metadata that affects generated MMDB bytes.
type MMDBOptions struct {
	// BuildEpoch is written to MMDB metadata as Unix epoch seconds. Zero keeps
	// the mmdbwriter default of using the conversion time.
	BuildEpoch int64
}

type mmdbTree interface {
	Insert(*net.IPNet, mmdbtype.DataType) error
	WriteTo(io.Writer) (int64, error)
}

type mmdbTreeFactory func(mmdbwriter.Options) (mmdbTree, error)

// DatToMMDB converts a geoip.dat file to MMDB format
func DatToMMDB(datPath, mmdbPath string) error {
	return DatToMMDBWithOptions(datPath, mmdbPath, MMDBOptions{})
}

// DatToMMDBWithOptions converts a geoip.dat file to MMDB format with explicit
// metadata options. A fixed BuildEpoch makes repeated conversions byte-stable.
func DatToMMDBWithOptions(datPath, mmdbPath string, options MMDBOptions) error {
	return datToMMDBWithOptions(datPath, mmdbPath, options, func(options mmdbwriter.Options) (mmdbTree, error) {
		return mmdbwriter.New(options)
	})
}

func datToMMDBWithOptions(
	datPath, mmdbPath string,
	options MMDBOptions,
	newTree mmdbTreeFactory,
) error {
	if options.BuildEpoch < 0 {
		return fmt.Errorf("build epoch must not be negative")
	}

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
	writer, err := newTree(
		mmdbwriter.Options{
			BuildEpoch:              options.BuildEpoch,
			DatabaseType:            "GeoIP2-Country",
			IncludeReservedNetworks: true,
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
	for entryIndex, entry := range geoIPList.Entry {
		if entry == nil {
			return fmt.Errorf("geoip entry %d is nil", entryIndex)
		}
		countryCode := entry.CountryCode

		// Create the record to insert
		record := mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(countryCode),
			},
		}

		// Insert each CIDR block
		for cidrIndex, cidr := range entry.Cidr {
			ipNet, err := parseCIDR(cidr)
			if err != nil {
				return fmt.Errorf(
					"geoip entry %d (%q) CIDR %d: %w",
					entryIndex,
					countryCode,
					cidrIndex,
					err,
				)
			}

			if err := writer.Insert(ipNet, record); err != nil {
				return fmt.Errorf(
					"insert geoip entry %d (%q) CIDR %d (%s): %w",
					entryIndex,
					countryCode,
					cidrIndex,
					ipNet.String(),
					err,
				)
			}
		}
	}

	if err := writeMMDBAtomically(mmdbPath, writer); err != nil {
		return fmt.Errorf("write MMDB output: %w", err)
	}

	return nil
}

func writeMMDBAtomically(mmdbPath string, writer mmdbTree) error {
	temp, err := os.CreateTemp(filepath.Dir(mmdbPath), "."+filepath.Base(mmdbPath)+".*")
	if err != nil {
		return fmt.Errorf("create temporary MMDB file: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := writer.WriteTo(temp); err != nil {
		return fmt.Errorf("write temporary MMDB data: %w", err)
	}
	if err := temp.Chmod(0o644); err != nil {
		return fmt.Errorf("set temporary MMDB permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary MMDB file: %w", err)
	}
	if err := os.Rename(tempPath, mmdbPath); err != nil {
		return fmt.Errorf("replace MMDB output: %w", err)
	}

	keepTemp = true
	return nil
}

// parseCIDR converts a pb.CIDR to a net.IPNet
func parseCIDR(cidr *pb.CIDR) (*net.IPNet, error) {
	if cidr == nil {
		return nil, fmt.Errorf("CIDR is nil")
	}
	ip := net.IP(cidr.Ip)
	if ip.To4() == nil && ip.To16() == nil {
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
