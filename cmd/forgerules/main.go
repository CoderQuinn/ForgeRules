package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/CoderQuinn/ForgeRules/pkg/geoip"
	"github.com/CoderQuinn/ForgeRules/pkg/geosite"
)

type cliDependencies struct {
	download           func(string, string, string, int64) error
	convertGeoSite     func(string, string) error
	convertGeoIP       func(string, string, geoip.MMDBOptions) error
	writeBuildMetadata func(string, string, sourceLock, string, string) error
	runtimeVersion     func() string
}

func productionDependencies() cliDependencies {
	return cliDependencies{
		download:           downloadVerifiedFile,
		convertGeoSite:     geosite.DatToJSON,
		convertGeoIP:       geoip.DatToMMDBWithOptions,
		writeBuildMetadata: writeBuildMetadata,
		runtimeVersion:     runtime.Version,
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func run(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	flags := flag.NewFlagSet("forgerules", flag.ContinueOnError)
	flags.SetOutput(stderr)
	geositeInput := flags.String("geosite-input", "", "Input geosite.dat file path")
	geositeOutput := flags.String("geosite-output", "geosite.json", "Output geosite.json file path")
	geoipInput := flags.String("geoip-input", "", "Input geoip.dat file path")
	geoipOutput := flags.String("geoip-output", "geoip.mmdb", "Output geoip.mmdb file path")
	geoipBuildEpoch := flags.Int64("geoip-build-epoch", 0, "MMDB build timestamp as Unix epoch seconds; 0 uses conversion time")
	sourcesLockPath := flags.String("sources-lock", "rules.sources.lock.json", "Pinned source lock used when no explicit inputs are provided")
	converterRevision := flags.String("converter-revision", "", "ForgeRules commit SHA recorded in locked-build provenance")

	flags.Usage = func() {
		fmt.Fprintln(stderr, "ForgeRules - Convert geosite.dat to JSON and geoip.dat to MMDB")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Usage:")
		fmt.Fprintln(stderr, "  forgerules [options]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Options:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  Convert geosite.dat to JSON:")
		fmt.Fprintln(stderr, "    forgerules -geosite-input=geosite.dat -geosite-output=geosite.json")
		fmt.Fprintln(stderr, "\n  Convert geoip.dat to MMDB:")
		fmt.Fprintln(stderr, "    forgerules -geoip-input=geoip.dat -geoip-output=geoip.mmdb")
		fmt.Fprintln(stderr, "\n  Convert both:")
		fmt.Fprintln(stderr, "    forgerules -geosite-input=geosite.dat -geoip-input=geoip.dat")
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Auto mode: no input provided → download only pinned upstream rules.
	if *geositeInput == "" && *geoipInput == "" {
		if *geoipBuildEpoch != 0 {
			fmt.Fprintln(stderr, "-geoip-build-epoch cannot override a pinned source lock")
			return 2
		}
		if !revisionPattern.MatchString(*converterRevision) {
			fmt.Fprintln(stderr, "-converter-revision must be a lowercase 40-character commit SHA")
			return 2
		}
		lock, err := loadSourceLock(*sourcesLockPath)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading source lock: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "No input specified. Using pinned source lock %s...\n", *sourcesLockPath)

		for _, src := range lock.Sources {
			fmt.Fprintln(stdout, "Processing:", src.Name)

			geositeDat := src.Name + "_geosite.dat"
			geoipDat := src.Name + "_geoip.dat"

			if err := dependencies.download(
				src.GeoSite.URL,
				geositeDat,
				src.GeoSite.SHA256,
				src.GeoSite.Size,
			); err != nil {
				fmt.Fprintf(stderr, "Error downloading %s geosite: %v\n", src.Name, err)
				return 1
			}
			if err := dependencies.download(
				src.GeoIP.URL,
				geoipDat,
				src.GeoIP.SHA256,
				src.GeoIP.Size,
			); err != nil {
				fmt.Fprintf(stderr, "Error downloading %s geoip: %v\n", src.Name, err)
				return 1
			}

			geositeJSON := src.Name + "_geosite.json"
			geoipMMDB := src.Name + "_geoip.mmdb"

			if err := dependencies.convertGeoSite(geositeDat, geositeJSON); err != nil {
				fmt.Fprintf(stderr, "Error converting %s geosite: %v\n", src.Name, err)
				return 1
			}
			if err := dependencies.convertGeoIP(
				geoipDat,
				geoipMMDB,
				geoip.MMDBOptions{BuildEpoch: src.GeoIP.BuildEpoch},
			); err != nil {
				fmt.Fprintf(stderr, "Error converting %s geoip: %v\n", src.Name, err)
				return 1
			}
		}
		if err := dependencies.writeBuildMetadata(
			".",
			*sourcesLockPath,
			lock,
			*converterRevision,
			dependencies.runtimeVersion(),
		); err != nil {
			fmt.Fprintf(stderr, "Error writing build metadata: %v\n", err)
			return 1
		}

		fmt.Fprintln(stdout, "Upstream conversion completed!")
		return 0
	}

	// Convert geosite.dat to JSON if input is provided
	if *geositeInput != "" {
		fmt.Fprintf(stdout, "Converting %s to %s...\n", *geositeInput, *geositeOutput)
		if err := dependencies.convertGeoSite(*geositeInput, *geositeOutput); err != nil {
			fmt.Fprintf(stderr, "Error converting geosite: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Successfully converted geosite to %s\n", *geositeOutput)
	}

	// Convert geoip.dat to MMDB if input is provided
	if *geoipInput != "" {
		fmt.Fprintf(stdout, "Converting %s to %s...\n", *geoipInput, *geoipOutput)
		if err := dependencies.convertGeoIP(
			*geoipInput,
			*geoipOutput,
			geoip.MMDBOptions{BuildEpoch: *geoipBuildEpoch},
		); err != nil {
			fmt.Fprintf(stderr, "Error converting geoip: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Successfully converted geoip to %s\n", *geoipOutput)
	}

	fmt.Fprintln(stdout, "\nConversion completed successfully!")
	return 0
}
