# ForgeRules

Convert V2Ray rule databases into formats usable by other clients and SDKs.

## What it does

* **geosite.dat → geosite.json**
  Decode V2Ray domain list (protobuf) into readable JSON

* **geoip.dat → geoip.mmdb**
  Convert V2Ray IP list into MaxMind MMDB (compatible with GeoIP2 readers)

## Build

Requires Go 1.24.12 or newer.

```bash
go build -o forgerules ./cmd/forgerules
```

## Usage

### Convert geosite

```bash
./forgerules -geosite-input geosite.dat
```

### Convert geoip

```bash
./forgerules -geoip-input geoip.dat
```

To make MMDB output reproducible, provide a fixed Unix build timestamp:

```bash
./forgerules -geoip-input geoip.dat -geoip-build-epoch 1700000000
```

With identical input bytes, converter version, and build epoch, repeated MMDB
conversions produce identical bytes. The default value `0` preserves the
existing behavior and records the conversion time.

### Convert both

```bash
./forgerules -geosite-input geosite.dat -geoip-input geoip.dat
```

Outputs:

```
geosite.json
geoip.mmdb
```

## Generated Artifacts (Latest Release)

### GeoSite(JSON), GeoIP (MMDB)

**Community**

https://github.com/CoderQuinn/ForgeRules/releases/latest/download/official_geosite.json
https://github.com/CoderQuinn/ForgeRules/releases/latest/download/official_geoip.mmdb

**Community enhanced**

https://github.com/CoderQuinn/ForgeRules/releases/latest/download/loyalsoldier_geosite.json
https://github.com/CoderQuinn/ForgeRules/releases/latest/download/loyalsoldier_geoip.mmdb

## Data Sources

Community:

* [https://github.com/v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)
* [https://github.com/v2fly/geoip](https://github.com/v2fly/geoip)

Community enhanced:

* [https://github.com/Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)

## Purpose

This project is intended as a preprocessing step for rule engines
(e.g. DNS routing / traffic classification).

For QuantumLink 0.8.0, ForgeRules is an offline production asset builder, not a
runtime dependency. Automatic conversion reads the reviewed
`rules.sources.lock.json`; it never follows mutable `latest` URLs. See
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the source and output
contracts.

## License

Apache 2.0
