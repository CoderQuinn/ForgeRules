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
- The workflow creates each dated release with a fresh REST `201` response; it
  never discovers or updates a draft by tag. It records that exact release ID,
  verifies the commit-bound draft and every remotely uploaded byte, and only
  then publishes it. After publication it requires the tag, release ID, and
  peeled commit to agree.
- `latest` is a separate fresh release, not a retagged dated release. The
  workflow creates a unique `latest-staging-RUN_ID-RUN_ATTEMPT` draft, uploads
  and byte-verifies all of its assets while the old `latest` remains available,
  and records its exact ID. Only then may it snapshot and recheck the old
  published `latest` release plus raw tag-ref object, delete that pair, retag
  the same verified staging ID to `latest` while it is still a draft, publish
  it with `make_latest=true`, and verify exact ID, latest pointer, exact tag
  ref, peeled commit, checksums, and remote bytes. A failure never exposes an
  unverified or partial `latest`, although `latest` can be temporarily absent
  after the old verified pair has been removed.
- Failure cleanup owns only the fresh REST response ID. A draft cleanup deletes
  that exact ID and never guesses ownership of a tag from a matching commit.
  Cleanup of an ambiguously published fresh release first withdraws the exact
  release ID, then deletes a tag only when the pre-delete release-by-tag ID and
  raw ref fingerprint still identify that same publication and the ref still
  peels to the expected commit. Mismatched or replaced refs are preserved for
  manual inspection.
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

## Repairing only the mutable latest alias

If a full run published and verified today's immutable dated release but failed
while replacing `latest`, do not rerun the full path: its write-once
`rules-YYYYMMDD` check must fail on the already-published tag. Instead, dispatch
**Build Rules** from a branch or tag that resolves to the intended commit and
select `release_scope` = `latest-only`:

```bash
gh workflow run build-rules.yml \
  --repo CoderQuinn/ForgeRules \
  --ref <branch-or-tag-at-intended-commit> \
  -f release_scope=latest-only
```

The recovery run still checks out that ref, builds, tests, converts, creates a
new unique staging draft, and verifies its remote bytes. It skips every dated
tag/create/publish/cleanup step and never reuses a failed staging draft by tag.
Confirm the run's `GITHUB_SHA` is the intended converter revision before using
the recovered alias.

Old-`latest` deletion is deliberately fail closed. The script accepts only
complete absence or one published, mutable release paired with an exact
`refs/tags/latest`; release-only, ref-only, hidden draft, immutable, and changed
identity states require manual repair. It snapshots and rechecks the release
fingerprint and raw ref object before mutation. GitHub's REST API does not offer
an atomic compare-and-delete for these objects, so an external writer can still
race between the last recheck and DELETE. The shared workflow concurrency group
serializes ForgeRules workflows, but operators should also restrict tag writes
and avoid other automation that mutates `latest`. If repository immutable
release policy prevents deleting and reusing `latest`, this mutable Release
alias workflow is unsupported; retain dated releases and choose a different
discovery mechanism rather than weakening the identity checks.

## Update procedure

1. Update `rules.sources.lock.json` in a reviewed pull request. Record each
   immutable upstream tag, resolved revision, publication time, size, SHA-256,
   and GeoIP build epoch.
2. Require the Go contract gate and pinned-source gate. The latter converts the
   complete locked inputs twice, validates both checksum files, and compares
   every output byte-for-byte.
3. Merge the reviewed change. The release workflow stages a new immutable
   `rules-YYYYMMDD` draft containing the four rule assets, the published source
   lock, `rules-manifest.json`, and `SHA256SUMS`; it publishes only after remote
   verification by release ID succeeds.
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
