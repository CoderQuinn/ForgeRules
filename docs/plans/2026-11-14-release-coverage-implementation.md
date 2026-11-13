# Release-script coverage implementation plan

Use subagent-driven development with GPT-6.1 SOL xhigh implementation and independent Astra high review. This continues the already authorized foundation scope.

Spec: [Release coverage](../specs/2026-11-14-release-coverage-design.md).

### Task 1: Reproducible release coverage and failure scenarios

Ownership: release test scripts/fixtures, new narrowly scoped coverage collector/fixtures, coverage documentation and this plan/spec. Read the concurrently completed bounded-input work, but do not edit its Go files or coverage-launcher package enumeration. Coordinate any shared launcher change first.

- [ ] Freeze executable-location and trace mapping with representative Bash syntax fixtures; retain measurement RED for missing/unexecuted code and misleading quote-only lines.
- [ ] Collect existing local release tests and report the initial uncovered locations. Do not treat kcov's naive multiline-string denominator as precise Bash coverage.
- [ ] Add table-driven failure scenarios checking mutation traces and final identities until production and changed executable gates pass. Keep test-support accounting explicit.
- [ ] Verify parser/collector negative cases, whole and changed coverage, existing release/workflow harnesses and committed-range whitespace/syntax checks. No live release calls.
- [ ] Commit owned files and report exact base/head, commands, raw evidence, coverage scopes and remaining limitations for independent Astra review.

## Scope ruling

Gate whole operational production Bash, added production rows since PR17, and new collector executable code at 95%. Report changed test/fixture scope separately with uncovered locations; intentional assertion-failure arms do not have a 95% requirement. No production file is excluded. Preserve negative collector and release mutation/identity checks rather than adding assertion-arm padding.

## Collector checkpoint

The collector and its negative/parity fixtures are reviewed separately before final fault-suite integration. Production and added-production Bash gates remain open until all suites are frozen and recollected against the exact final commit. See the design for ownership, native syntax hashes, conservative ambiguity/EXIT behavior, provenance binding and separate Python tooling gate.

Independent collector review requested corrections for actual sourced-file identity, transient source changes, per-child Bash pinning and redirect-only ownership. The fix slice retains RED evidence and separate re-review before final collection. All earlier numeric release reports are diagnostic, not qualification.
