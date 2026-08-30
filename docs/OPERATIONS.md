# Rule Asset Update and Rollback Operations

## Scope and ownership

ForgeRules produces immutable, reviewable rule-asset releases for QuantumLink
0.8.0. It does not install assets and is never linked into the runtime.
ForgeRuleCore validates and loads the selected `geosite.json` and `geoip.mmdb`;
the QuantumLink host owns staging, atomic activation, the applied-revision
receipt, and rollback.

## Safety invariants

- A production activation uses an immutable `rules-YYYYMMDD` release. The
  mutable `latest` release is for discovery only and is never an activation or
  rollback identifier.
- A dated release tag is write-once. Release workflows fail if that tag already
  exists; they never replace its assets.
- The workflow creates and verifies the immutable dated release before it
  replaces `latest`. Verification downloads the published asset set, compares
  every byte with the build output, and validates `SHA256SUMS`.
- Every downloaded file is verified against `SHA256SUMS`. The manifest's
  converter revision, source-lock digest, selected bundle, sizes, and digests
  must agree with the downloaded files before runtime validation begins.
- Validation and installation happen in a staging directory. A failed download,
  checksum, decode, MMDB open, or smoke decision leaves the active bundle and
  last-known-good receipt unchanged.
- The active and immediately previous accepted bundles remain available on
  disk. Cleanup may remove older inactive bundles only after a newer bundle has
  been accepted and the previous bundle is still intact.
- Update failure is observable and must not silently select Direct routing.

## Update procedure

1. Update `rules.sources.lock.json` in a reviewed pull request. Record each
   immutable upstream tag, resolved revision, publication time, size, SHA-256,
   and GeoIP build epoch.
2. Require the Go contract gate and pinned-source gate. The latter converts the
   complete locked inputs twice, validates both checksum files, and compares
   every output byte-for-byte.
3. Merge the reviewed change. The release workflow creates a new immutable
   `rules-YYYYMMDD` release containing the four rule assets, the published
   source lock, `rules-manifest.json`, and `SHA256SUMS`.
4. Download the dated release to a new staging directory and verify it before
   selecting a bundle:

   ```bash
   release_tag=rules-20260830
   staging_directory="$(mktemp -d /tmp/forgerules-${release_tag}.XXXXXX)"
   gh release download "${release_tag}" \
     --repo CoderQuinn/ForgeRules \
     --dir "${staging_directory}"
   (cd "${staging_directory}" && shasum -a 256 -c SHA256SUMS)
   jq -e \
     '.schemaVersion == 1
      and (.converter.revision | test("^[0-9a-f]{40}$"))' \
     "${staging_directory}/rules-manifest.json"
   ```

5. Copy only the selected bundle's GeoSite and GeoIP files into a separate host
   staging directory. Open both through the release ForgeRuleCore build and run
   deterministic smoke decisions that cover the expected direct, proxy, and
   reject paths.
6. After every check succeeds, atomically activate the staged `Rules/`
   directory (or atomically update an equivalent host-owned pointer). Record an
   applied-revision receipt and keep the prior receipt and bundle unchanged.

The host-owned receipt must contain enough information to reproduce or roll
back the exact selection:

```json
{
  "schemaVersion": 1,
  "releaseTag": "rules-20260830",
  "converterRevision": "88f616066bec5d516076c166ae41ff44749d690b",
  "manifestSHA256": "e097b0fccb95cb146565e3db96defcba5de90488757b41bf840996ccbab89392",
  "bundle": "official",
  "geositeSHA256": "985b6b0e0f40491de4c97015e3ef306ed196646998cb8dfb190b6f7f4cec7720",
  "geoipSHA256": "c6756d3d9bc7cf7a35d305117bc4299dae6a74d88b8f815a38b18e4d7afb0885"
}
```

The example is the observed `rules-20260830` release. An implementation may add
timestamps or host metadata, but it must not replace these immutable identity
fields with a `latest` URL.

## Last-known-good definition

The last-known-good (LKG) bundle is the most recent dated release that passed
all checksum, manifest, ForgeRuleCore load, and host smoke checks and was then
atomically activated. A downloaded, published, or merely decoded bundle is not
LKG. The LKG identity is the complete receipt above, not only a date or mutable
release URL.

## Rollback procedure

1. Stop the failing update or mark the active receipt unhealthy; do not mutate
   the dated release or regenerate its assets.
2. Select the immediately previous accepted receipt and its retained immutable
   bundle. If the local copy is unavailable, download that exact dated release.
3. Re-run `SHA256SUMS`, manifest, ForgeRuleCore load, and smoke checks against
   the receipt values.
4. Atomically reactivate the verified previous `Rules/` directory and persist a
   new activation record that references the restored receipt.
5. Preserve failure diagnostics without raw configuration, private domains,
   credentials, or asset contents. A failed rollback leaves the current LKG
   untouched and reports an explicit unavailable state to the host.

Deleting a dated release, moving a dated tag, copying assets from `latest`, or
mixing GeoSite and GeoIP files from different receipts is not a rollback.
