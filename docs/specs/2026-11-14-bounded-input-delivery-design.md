# ForgeRules bounded-input delivery

Status: implementation authorized on 2026-11-14 for this foundation batch. Tracks [#8](https://github.com/CoderQuinn/ForgeRules/issues/8) and preserves [#17](https://github.com/CoderQuinn/ForgeRules/pull/17).

Candidate baseline: PR17 exact head `1dc34fed52480cad816843e66495461a3058dbef`; main `c8b96970f4ca032765a615bf51d7330d2a854be1`. Strict enum/CIDR validation and immutable GitHub release scripts already exist; do not duplicate them or update the shared PR branch without controller coordination.

## Required behavior

Bound local GeoSite/GeoIP protobuf reads before unmarshal and source-lock JSON reads before decode/provenance reread. Preserve public converter signatures; use one shared first-party bounded-read helper where suitable. Freeze separate documented default limits after checking the pinned sources' declared sizes, with tests for empty, exact limit, one byte over, read failure and oversized sparse/streamed input. Stat alone is insufficient because input may grow. Do not allocate the entire over-limit file to discover its size. Locked download declarations must obey the corresponding absolute ceiling before size+1 arithmetic/read. Output must remain byte-for-byte unchanged on invalid CIDR/unknown enum/oversized input/conversion or write failure.

If the shared helper is under internal/, extend the production coverage enumerator and coverpkg roots so all new authored code is measured. Preserve the existing coverage semantics and disclose the Go profiler's uninstrumented initializer limitation.

Confirm same pins/build epoch yield byte-identical artifacts and existing release scripts reject corrupt manifest/second artifact, incomplete assets, same-name release and interrupted promotion. Run existing release/recovery harnesses; fix only evidenced gaps within this slice. Real reader qualification is explicitly blocked until its own clean consumable baseline exists; producer golden tests must not pretend to exercise that reader.

Whole-output-directory atomic promotion is a separate independently reviewable slice, because current flat-output CLI mutates GeoSite before GeoIP succeeds. Document this gap precisely and do not claim per-file atomic writes guarantee transaction/power-loss atomicity. A new directory/pointer API requires its own brief before implementation; no improvised incompatible change inside this bounded-input slice.

Keep existing CLI, formats, source lock, download hashes and immutable release semantics. Synthetic fixtures only. No upstream lock refresh, network release, merge or tag. Rollback reverts additive limit enforcement; published bundles are never overwritten.

## Frozen input budgets (2026-11-14, before production changes)

Public GitHub release metadata was read for the exact tags already in
`rules.sources.lock.json`; asset sizes match every declaration in that lock:

| Input | Pinned tag | Declared bytes | Public evidence |
| --- | --- | ---: | --- |
| Official GeoSite `dlc.dat` | `20260827152101` | 2,283,258 | [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community/releases/tag/20260827152101) |
| Official GeoIP `geoip.dat` | `202608050239` | 23,080,744 | [v2fly/geoip](https://github.com/v2fly/geoip/releases/tag/202608050239) |
| Loyalsoldier GeoSite `geosite.dat` | `202608290326` | 11,005,730 | [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202608290326) |
| Loyalsoldier GeoIP `geoip.dat` | `202608290326` | 17,224,069 | [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat/releases/tag/202608290326) |

Freeze separate inclusive ceilings at **32 MiB (33,554,432 bytes) for GeoSite**,
**64 MiB (67,108,864 bytes) for GeoIP**, and **1 MiB (1,048,576 bytes) for source-lock
JSON**, including the provenance reread. The protobuf ceilings leave approximately
3.05x and 2.91x headroom above the largest current pins. The reviewed lock is
1,951 bytes; its separate ceiling permits substantial provenance growth without
letting JSON whitespace or fields consume an unbounded buffer. These are absolute
input-byte ceilings, not heap or decoded-record-count guarantees. Future sources
that exceed them need an explicitly reviewed budget change; there is no CLI
bypass or automatic increase.

Source-lock validation rejects each declared size above its kind's ceiling.
The existing generic downloader also rejects declarations above the largest
64 MiB ceiling before a request or `size+1` arithmetic, preserving its interface.
All accepted downloads remain limited to their exact declared size plus one probe
byte and require the existing size/hash checks. Local regular-file size checks
may reject early, but every protobuf/JSON stream must still use a limit-plus-one
read so growth after stat and unknown-length input cannot bypass the budget.
Generated artifact hashing remains streaming with bounded working memory; it is
not an untrusted protobuf/JSON input decode and retains its existing byte-count
and digest behavior. Release metadata checks do not claim downloaded-byte/hash
revalidation of the public source assets.

## Candidate review corrections (separate slice, execute first)

Astra's 2026-11-14 exact-head review reproduced two failures in PR17. These are inside the existing fail-closed/immutable release contract and need fixes before candidate readiness:

1. Every release/tag fingerprint calculation must propagate failure explicitly before comparison or destructive requests. Unsupported raw ref types, malformed fingerprints and recheck failures must exit nonzero. Initial snapshot/recheck failure permits no DELETE/PATCH; after a prior successful mutation, stop further mutation and preserve any remaining object. Assignment through readonly/local declarations or inside comparisons must not mask status.
2. Once a dated rules-YYYYMMDD name was publicly published, withdrawal must not free it for a different commit. Preserve its exact dated tag reservation when withdrawing the failed release; a subsequent same-name draft creation at another revision must fail. Keep staging draft cleanup and mutable latest behavior separately defined. Unknown/changed associations remain fail-closed, and no changed/unowned ref may be deleted. Missing reservation requires explicit failure/manual recovery, not a success claim.

The correction also includes minimal explicit status guards in `Scripts/lib/github-release.sh`: conditional callers suppress Bash errexit, so SHA validation and annotated-tag API failures must return before producing a fingerprint or peeled commit.

Use existing fake-gh/fake-curl fixtures for deterministic mutation-trace tests; no live release mutation. Update operations/recovery docs and tests that incorrectly expected published dated tags to be deleted. Do not weaken exact-ID or annotated-tag peeling checks. This correction preserves the existing immutable identity promise; it is not a new artifact format.
