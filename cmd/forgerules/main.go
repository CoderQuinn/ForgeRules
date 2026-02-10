package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/CoderQuinn/ForgeRules/pkg/geoip"
	"github.com/CoderQuinn/ForgeRules/pkg/geosite"
)

func main() {
	// Define command-line flags
	geositeInput := flag.String("geosite-input", "", "Input geosite.dat file path")
	geositeOutput := flag.String("geosite-output", "geosite.json", "Output geosite.json file path")
	geoipInput := flag.String("geoip-input", "", "Input geoip.dat file path")
	geoipOutput := flag.String("geoip-output", "geoip.mmdb", "Output geoip.mmdb file path")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ForgeRules - Convert geosite.dat to JSON and geoip.dat to MMDB\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Convert geosite.dat to JSON:\n")
		fmt.Fprintf(os.Stderr, "    %s -geosite-input=geosite.dat -geosite-output=geosite.json\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Convert geoip.dat to MMDB:\n")
		fmt.Fprintf(os.Stderr, "    %s -geoip-input=geoip.dat -geoip-output=geoip.mmdb\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Convert both:\n")
		fmt.Fprintf(os.Stderr, "    %s -geosite-input=geosite.dat -geoip-input=geoip.dat\n\n", os.Args[0])
	}

	flag.Parse()

	// Check if at least one input is provided
	if *geositeInput == "" && *geoipInput == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Convert geosite.dat to JSON if input is provided
	if *geositeInput != "" {
		fmt.Printf("Converting %s to %s...\n", *geositeInput, *geositeOutput)
		if err := geosite.DatToJSON(*geositeInput, *geositeOutput); err != nil {
			fmt.Fprintf(os.Stderr, "Error converting geosite: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully converted geosite to %s\n", *geositeOutput)
	}

	// Convert geoip.dat to MMDB if input is provided
	if *geoipInput != "" {
		fmt.Printf("Converting %s to %s...\n", *geoipInput, *geoipOutput)
		if err := geoip.DatToMMDB(*geoipInput, *geoipOutput); err != nil {
			fmt.Fprintf(os.Stderr, "Error converting geoip: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully converted geoip to %s\n", *geoipOutput)
	}

	fmt.Println("\nConversion completed successfully!")
}
