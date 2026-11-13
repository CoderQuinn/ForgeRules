# ForgeRules foundation implementation plan

> Use superpowers:subagent-driven-development. Begin only after coordinator confirms G0 review complete.

**Goal:** deliver this bounded foundation increment with reproducible evidence.
**Architecture:** preserve current production contracts; add verification/tooling and narrowly reproduced fixes.
**Tech Stack:** existing repository toolchain.
**Spec:** [Design](../specs/2026-11-14-bounded-input-delivery-design.md).

## Global constraints

- Preserve existing public API and original dirty workspaces; work only in the assigned isolated checkout.
- No unrelated dependencies, business routing decisions or host features. Only synthetic fixtures and public repository facts may be committed.
- First-party production and changed executable lines each >=95%; report numerator/denominator and authored C/ObjC/Swift/Go scope explicitly. Do not hide a shortfall with exclusions.
- Unknown/blocked/not_run is not passed. Record exact base..head, toolchain, commands and consumer pins.
- Implementation: GPT-6.1 SOL xhigh. Independent spec and quality review: Astra high. No merge, bypass, tags or release.
- Same-machine performance runs are serialized by the coordinator; building/testing may run concurrently, timing claims must wait for exclusive access.

## Review Focus

- Evidence generated from a different revision or dirty input being presented as qualified.
- Error handling that changes existing outputs or leaks borrowed memory/secrets.
- Duplicate tests that miss actual API/adaptor boundaries.
- Coverage exclusions concealing authored code and inherited changes being credited as new.
- Missing build/reader/performance capability being labeled passed.

### Task 1: Bounded converter inputs and exact-candidate validation

**Files:** `pkg/{geoip,geosite}/converter.go`, `cmd/forgerules/{sources,manifest,download}.go` and corresponding tests, a small shared `internal/` bounded-read helper/tests if needed, README, existing coverage/CI scripts to account for any new helper. Do not change source pins/generated protobuf.
**Interfaces:** existing `DatToJSON`, `DatToMMDBWithOptions`; additive internal helper/error contract, no public signature break.

- [ ] Freeze default byte budget from pinned-source evidence in spec; inspect PR17 tests and baseline gates. Record main..PR17 as inherited, new base..head separately.
- [ ] RED: oversize input is rejected before unmarshal and old output unchanged; reader limit exact and +1, growing/sparse inputs, read errors. Invalid enum/CIDR and write failures retain existing tests.
- [ ] Implement bounded reading with deterministic actionable errors and no full over-limit allocation; preserve converter compatibility for valid data.
- [ ] GREEN: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/forgerules`, `bash Scripts/check-go-coverage.sh`, existing Scripts/test-* release/workflow harnesses, changed-line audit. Same-pin deterministic fixture rebuild.
- [ ] Commit and report exact coverage and inherited vs new diff. Record reader integration and whole-bundle atomicity as explicit outstanding gates; do not close umbrella #8.

### Task 2: Fail-closed cleanup and permanent published dated identity

Execute this correction before Task 1; keep its commit and review range separate.

**Files:** `Scripts/delete-latest-release.sh`, `Scripts/cleanup-failed-draft-release.sh`, `Scripts/lib/github-release.sh`, `Scripts/test-verify-release-assets.sh`, `Scripts/fixtures/fake-gh-release.sh` only if fixture support is needed, `docs/OPERATIONS.md` and related release test expectations.
**Interfaces:** unchanged shell CLI arguments and existing GitHub REST endpoints; checked nonempty fingerprints and preserved dated-tag reservation.

- [x] Reproduce both Astra findings against exact PR17 using synthetic fixtures. Assert exit status and absence/preservation of DELETE/PATCH effects, not only stderr text.
- [x] Check snapshot and every recheck assignment status explicitly; mark readonly only after successful validation. Reject unsupported/malformed/failed fingerprints at every destructive boundary.
- [x] Preserve published dated tag identity after withdrawal. Prove same-name/different-commit draft recreation fails; keep genuine unpublished staging and mutable latest cleanup tests passing. Missing or changed association/ref remains explicit failure and is preserved.
- [x] Run `bash -n` on edited scripts and all three existing release/workflow harnesses. No network releases. Commit fix separately and report exact candidate/fix range and test results for Astra re-review.
