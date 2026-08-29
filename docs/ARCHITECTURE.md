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
GeoIP epochs that do not match `publishedAt`. Downloads are written to a
temporary file, size- and digest-verified, and atomically renamed so a failed
update cannot overwrite a previously valid local file.

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

The rollback pointer and cross-repository ForgeRuleCore golden acceptance remain
separate delivery gates; this document does not claim those later gates are
complete.
