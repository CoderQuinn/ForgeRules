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

## Test and coverage gate

```bash
./Scripts/check-go-coverage.sh
```

The gate requires at least 95% unique executable-line coverage across every
active handwritten Go production file reported by `go list` for
`cmd/forgerules` and `pkg`. An executable line is a unique, nonempty source line
containing a non-comment, non-structural Go token inside an executable AST
statement or expression and owned by a coverage-profile block whose statement
count is greater than zero. This includes the continuation lines of multiline
calls, conditions, literals, and return operands; braces, comments, case/select
labels, and punctuation-only lines do not count. A line is covered when any
owning executable block ran, and every executable token must map to such a block
so a truncated profile fails closed. The report lists uncovered `file:line`
locations deterministically. This is block-owned token-line coverage, not branch
coverage. The gate still compiles and tests `./...`; only
`proto/*.pb.go` is outside the measured roots because those files are generated
from the checked-in `.proto` contracts. It does not filter individual
production files or remove uncovered executable lines.

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

Pinned automatic builds also emit `rules-manifest.json` and `SHA256SUMS`. The
manifest records the converter revision and toolchain, the complete locked input
provenance, and each output's format, size, and SHA-256 digest. CI runs the
pinned build twice and requires byte-identical files.

## Generated Artifacts (Latest Release)

### GeoSite(JSON), GeoIP (MMDB)

**Community**

https://github.com/CoderQuinn/ForgeRules/releases/latest/download/official_geosite.json
https://github.com/CoderQuinn/ForgeRules/releases/latest/download/official_geoip.mmdb

**Community enhanced**

https://github.com/CoderQuinn/ForgeRules/releases/latest/download/loyalsoldier_geosite.json
https://github.com/CoderQuinn/ForgeRules/releases/latest/download/loyalsoldier_geoip.mmdb

**Provenance and verification**

https://github.com/CoderQuinn/ForgeRules/releases/latest/download/rules-manifest.json
https://github.com/CoderQuinn/ForgeRules/releases/latest/download/SHA256SUMS

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
contracts and [`docs/OPERATIONS.md`](docs/OPERATIONS.md) for immutable dated
releases, staged activation, last-known-good receipts, and rollback. The
[`public-data audit`](docs/PUBLIC-DATA-AUDIT.md) describes why the automatic
release path cannot ingest operator configuration, credentials, or private
endpoints.

## License

Apache 2.0
