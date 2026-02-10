# ForgeRules

A Go-based tool for converting geolocation and geosite data between different formats:

- **geosite.dat → geosite.json**: Convert V2Ray binary geosite data to human-readable JSON format
- **geoip.dat → geoip.mmdb**: Convert V2Ray binary geoip data to MaxMind MMDB format

## Features

- ✅ Convert geosite.dat (V2Ray protobuf format) to JSON
- ✅ Convert geoip.dat (V2Ray protobuf format) to MMDB (MaxMind database format)
- ✅ Supports domain types: plain, regex, domain, full
- ✅ Preserves domain attributes in JSON output
- ✅ Generates GeoIP2-compatible MMDB files
- ✅ Fast and efficient conversion
- ✅ Simple CLI interface

## Installation

### Prerequisites

- Go 1.19 or later

### Build from Source

```bash
git clone https://github.com/CoderQuinn/ForgeRules.git
cd ForgeRules
go build -o forgerules ./cmd/forgerules
```

## Usage

### Convert geosite.dat to JSON

```bash
./forgerules -geosite-input=geosite.dat -geosite-output=geosite.json
```

Output format:
```json
{
  "geosites": [
    {
      "country_code": "category-ads",
      "domains": [
        {
          "type": "domain",
          "value": "example.com",
          "attributes": {
            "cn": true
          }
        }
      ]
    }
  ]
}
```

### Convert geoip.dat to MMDB

```bash
./forgerules -geoip-input=geoip.dat -geoip-output=geoip.mmdb
```

The generated MMDB file is compatible with MaxMind GeoIP2 readers and can be used with tools like:
- `mmdbinspect`
- `geoiplookup`
- MaxMind GeoIP2 libraries

### Convert Both Formats

```bash
./forgerules -geosite-input=geosite.dat -geoip-input=geoip.dat
```

### All Options

```
  -geosite-input string
        Input geosite.dat file path
  -geosite-output string
        Output geosite.json file path (default "geosite.json")
  -geoip-input string
        Input geoip.dat file path
  -geoip-output string
        Output geoip.mmdb file path (default "geoip.mmdb")
```

## Input File Sources

You can download geosite.dat and geoip.dat files from:

- V2Ray project: https://github.com/v2fly/geoip
- V2Ray domain list: https://github.com/v2fly/domain-list-community

Example:
```bash
# Download geosite.dat
wget https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat -O geosite.dat

# Download geoip.dat
wget https://github.com/v2fly/geoip/releases/latest/download/geoip.dat
```

## Use Cases

- **Proxy Tools**: Convert data for use in Clash, Surge, Shadowrocket, sing-box
- **Network Filtering**: Extract and analyze domain/IP lists
- **GeoIP Services**: Create custom GeoIP databases
- **Data Analysis**: Convert binary data to JSON for easier processing
- **Routing Rules**: Generate routing rules from V2Ray format

## Technical Details

### geosite.dat Format

- Binary protobuf format from V2Ray
- Contains categorized domain lists
- Supports multiple domain match types:
  - **plain**: Substring match
  - **regex**: Regular expression match
  - **domain**: Domain and subdomains match
  - **full**: Exact match
- Can include custom attributes per domain

### geoip.dat Format

- Binary protobuf format from V2Ray
- Contains country/region IP CIDR blocks
- Supports both IPv4 and IPv6

### MMDB Format

- MaxMind database format
- Compact and efficient binary format
- Fast IP lookup performance
- Compatible with GeoIP2/GeoLite2 tooling

## Dependencies

- [google.golang.org/protobuf](https://pkg.go.dev/google.golang.org/protobuf) - Protocol Buffers
- [github.com/maxmind/mmdbwriter](https://pkg.go.dev/github.com/maxmind/mmdbwriter) - MMDB file writer
- [github.com/oschwald/maxminddb-golang](https://pkg.go.dev/github.com/oschwald/maxminddb-golang) - MMDB reader

## License

Apache License 2.0 - see [LICENSE](LICENSE) file for details

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

## Related Projects

- [v2fly/geoip](https://github.com/v2fly/geoip) - V2Ray GeoIP database
- [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community) - V2Ray domain list
- [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat) - Enhanced V2Ray routing rules
