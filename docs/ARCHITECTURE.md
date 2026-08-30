# ForgeRules Architecture

## Role in QuantumLink 0.8.0

ForgeRules is the required offline producer for reviewable QuantumLink 0.8.0
production rule assets. It is not a runtime dependency. QuantumLink ships or
imports the generated `geosite.json` and `geoip.mmdb`; ForgeRuleCore remains the
only runtime evaluator of those files.

Normal app builds and tests may use small audited fixtures without running this
repository. A production rule-asset update is complete only after the pinned
ForgeRules pipeline has produced and verified the release artifacts.

## Dependency direction

```text
pinned upstream releases -> ForgeRules -> geosite.json + geoip.mmdb
                                           |
                                           v
                                      ForgeRuleCore
                                           |
                                           v
                                 QuantumLink App Group Rules/
```

ForgeRules must not import ForgeRuleCore, NetForge, TunForge, or QuantumLink.
It owns source acquisition and format conversion, not matching or routing
semantics.

## Pinned input contract

`rules.sources.lock.json` is the reviewed source-of-truth for automatic builds.
Each asset records:

- upstream repository, immutable release tag, and resolved commit revision;
- release publication time;
- tag-specific HTTPS asset URL;
- byte size and SHA-256 digest reported by the upstream GitHub release;
- for GeoIP, the release timestamp expressed as `buildEpoch` for deterministic
  MMDB metadata.

The CLI rejects unknown lock fields, unsupported schema versions, mutable or
inconsistent URLs, invalid revisions/digests/sizes, duplicate source names, and
GeoIP epochs that do not match `publishedAt`. Automatic builds accept only the
reviewed public repositories and asset names listed in the validator; source
URLs cannot contain user information, query parameters, or fragments. Downloads
are written to a temporary file, size- and digest-verified, and atomically
renamed so a failed update cannot overwrite a previously valid local file.

Updating a dataset requires a reviewed lock-file change with the new release
tag, resolved commit, published time, size, and digest. Editing only the URL or
using a `latest` release URL is not supported.

## Output contract

For each source named `<name>`, ForgeRules emits:

- `<name>_geosite.json`
- `<name>_geoip.mmdb`

Given identical locked input bytes, converter revision, Go toolchain, and GeoIP
build epoch, the output bytes are deterministic. The repository pins the Go
toolchain in `go.mod`, and CI selects it through `go-version-file`.

Pinned builds also emit:

- `rules-manifest.json`, which records the manifest schema, converter repository
  and revision, Go version, source-lock digest and contents, and every output's
  format, size, digest, build epoch, and input provenance;
- `SHA256SUMS`, which covers the source lock, manifest, and all generated rule
  assets.

The pinned-source CI gate performs the complete conversion twice at the same
revision, verifies both checksum files, and requires every published file to be
byte-identical. The release workflows publish the manifest and checksums beside
the rule assets.

`testdata/golden/geosite.json` freezes the byte-level GeoSite v1 contract. Its
Go test generates the JSON from protobuf input and compares exact bytes. The
same SHA-256-pinned fixture is loaded by ForgeRuleCore bundle tests, so the
producer and evaluator share a real contract without creating a runtime or
package dependency.

The cross-repository ForgeRuleCore golden acceptance is pinned by SHA-256 and
loaded in ForgeRuleCore CI. Dated release tags are immutable, while the host
owns atomic activation and the applied-revision pointer. See
[`OPERATIONS.md`](OPERATIONS.md) for the update, last-known-good, and rollback
contract and [`PUBLIC-DATA-AUDIT.md`](PUBLIC-DATA-AUDIT.md) for the release-data
privacy boundary.
