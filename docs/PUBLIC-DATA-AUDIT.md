# Public Release Data Audit

## Claim and boundary

ForgeRules release workflows publish only deterministic transformations of the
public, immutable upstream assets named in `rules.sources.lock.json`. They do
not read QuantumLink configuration, App Group contents, environment-provided
rule paths, or operator credentials. This is a release-pipeline claim, not a
claim that public routing datasets never contain rules for reserved networks.

## Enforced inputs

The automatic converter path accepts only these reviewed public sources and
asset names:

- `v2fly/domain-list-community`: `dlc.dat`
- `v2fly/geoip`: `geoip.dat`
- `Loyalsoldier/v2ray-rules-dat`: `geosite.dat` and `geoip.dat`

GitHub reported all three repositories as public, non-private, and non-archived
on 2026-08-30.

Each URL must be a matching immutable GitHub release URL with no embedded user
information, query parameters, or fragment. Each downloaded file must also
match its reviewed byte size and SHA-256 digest. Adding another repository or
asset therefore requires a validator change, behavior tests, and review; a
lock-file-only change cannot introduce a private endpoint or credential-bearing
URL.

The release workflows invoke the converter without explicit input paths, so
they always take this locked automatic path. The published source lock and
manifest contain only the validated public provenance and generated artifact
metadata.

## Repository-owned examples and fixtures

The tracked GeoSite golden uses the IANA-reserved `.test` namespace. GeoIP
tests use RFC 1918 addresses to verify that a standard `PRIVATE` category is
preserved. These are synthetic protocol fixtures, not observed operator data.

An audit of tracked first-party text on 2026-08-30 found no private keys,
credential values, private service endpoints, or non-test private domain names.
Public documentation URLs, source-release URLs, the reserved `.test` fixture,
RFC 1918 converter tests, and the literal `redacted` in rejection tests were the
only relevant literals.

## Residual risk and update rule

Public upstream rule collections can intentionally include reserved-network or
private-category entries, and an upstream maintainer could publish unwanted
content. Source updates remain reviewed and pinned by revision, size, and
digest; the pinned-source gate converts the inputs twice and records the exact
provenance. Consumers should still inspect policy changes before activating a
new dated release. ForgeRules must never accept a user or organization-private
dataset in the production release workflow.
