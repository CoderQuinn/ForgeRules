# Release-script coverage evidence

This is the verification follow-up to the reviewed release-identity corrections in the bounded foundation delivery. It is part of issue #8; it does not add a release operation or authorize publishing.

## Contract

The Go coverage gate does not establish coverage of Bash release orchestration. Establish a reproducible numeric Bash command-source-line metric for the authored production scripts in `Scripts/` and `Scripts/lib/`, including the coverage launcher. Report every file and the aggregate, and separately report additions against the pre-correction revision `1dc34fed52480cad816843e66495461a3058dbef`. Production and added executable lines must reach 95%; preserve an explicit failing gate until they do. Changed test/fixture and coverage-tool code must be accounted for separately rather than silently omitted.

Use syntax-derived executable locations and actual process traces, with a pinned parser/tool version. Quoted jq programs, JSON fixtures, comments and syntax-only delimiters are not independent Bash commands. State that this metric does not measure jq branches or count quoted text as executed Bash. Cover nested command substitutions, sourced functions, multiline conditions and redirected child processes in the measurement tool's fixtures. Missing, malformed or stale source/trace evidence must fail closed. Record source hashes, base/head, tool versions and raw evidence. Validate the collector with deliberate missing and uncovered command probes before trusting a percentage. Do not apply line-pattern exclusions to improve a result.

Extend deterministic local fake-GitHub/fake-curl scenarios to cover meaningful failure and recovery paths. Assertions must check exit status, permitted API mutations, and retained release/ref identity. Prioritize ambiguous deletion, changed or missing reservations, malformed metadata, authentication/API failures and retry limits. All release tests must stay local and synthetic. No token or real release API is needed. Preserve the behavior already independently reviewed, including retained published dated reservations.

Keep the converter input task's Go files, source pins, generated protobufs and production formats outside this follow-up. A reproduced production script defect must be reported and corrected as an explicit fix with RED evidence; instrumentation alone is not justification for changing runtime behavior. Do not alter shell startup files, signing, repository protection or release configuration to make measurements pass.

## Acceptance

The report separates whole production, changed production, changed test/support and collector scopes, with exact numerators, denominators and uncovered locations. Verification includes the existing release harnesses, focused failure cases, collector negative probes, and committed-range whitespace/syntax checks. Independent Astra review must assess both metric validity and mutation safety. If any required scope is below 95%, report it as open rather than substituting another language's percentage. The original delivery remains unmerged while required gates are open.

## Scope ruling

The mandatory 95% gates are the aggregate of all first-party operational production Bash, its added executable rows since `1dc34fed52480cad816843e66495461a3058dbef`, and the new collector's executable code. Test and fixture coverage is fully reported separately, including intentional uncovered assertion-failure arms. Do not add meta-tests solely to execute those arms or use passing test code to inflate production coverage. Collector negative cases and release mutation/identity oracles must provide meaningful evidence.

## Frozen measurement semantics

The parser is shfmt **v3.12.0**, Bash dialect. Native tracing uses **GNU Bash 5.3.15** DEBUG/functrace events. kcov 43 was investigated, but its XML drops some real multiline-substitution closing-row hits and includes quoted jq text in its denominator; it is diagnostic only. Native tracing does not require kcov or its file-descriptor workaround.

An executable command owns unique physical rows on which its AST command token, argument/assignment/array element, test operand/operator or redirection starts. A multiline argument's quoted interior is data, not additional Bash rows. Simple commands, declarations, tests, arithmetic, and evaluated for/case/time/coproc headers are counted. Function declarations, container delimiters and comments add no independent rows; their executable child commands still count. The `go list` and `go test` command owners include the added `internal/...` continuation arguments. This is command-source-row coverage, not branch coverage or jq/Python-in-string coverage.

Each native event records source identity, Bash runtime row and unexpanded command syntax temporarily. Pinned shfmt normalization plus position-free AST hashing yields the retained syntax fingerprint; temporary syntax text is removed after finalization. Runtime rows must match the command's start/end or an enclosing command/process substitution's closing row. Identical commands that remain ambiguous at a shared runtime row earn no coverage and are reported explicitly. Bash can retain a stale `BASH_COMMAND` inside EXIT handlers; unmatched handler rows remain conservatively uncovered, never credited by guessing a whole span. The denominator retains them.

All `Scripts/**/*.sh` files are enumerated: operational files are production; `test-*` and `fixtures/` are separately reported support. No production file or uncovered row is excluded. Source SHA-256 manifests include shell and Python tooling/support; raw event and trace-helper hashes, exact base/head, versions, exit status and run timestamps bind each collection. A copied synthetic fixture is mapped to its authored source only by its runtime-recorded complete-file digest; unknown external sources are reported. Missing, malformed, changed-during-run and stale evidence fails closed. Empty denominators are N/A (`null`), never measured 100%.

The collector's Python executable rows use CPython compiler line tables recursively across nested code objects, with actual stdlib `trace` counters from its tests. Its dedicated gate requires 95%; its tests and the release-fault Python suite are reported separately with all uncovered rows. This is line coverage, not branch coverage.

## Reproduction commands

Put the pinned Bash and shfmt binaries on `PATH`, or provide `SHFMT` for the parser. No global shell/profile change is required.

```bash
FORGERULES_PYTHON_COVERAGE=/tmp/collector-coverage.json python3 Scripts/test_release_coverage.py
python3 Scripts/release_coverage.py collect /tmp/release-trace --entry Scripts/test-verify-release-assets.sh
python3 Scripts/release_coverage.py report /tmp/release-trace > /tmp/release-coverage.json
```

The report command enforces the production and added-production Bash thresholds. It must be paired with the collector test/coverage command above; a Bash percentage cannot replace the collector's separate gate. Multiple `collect` directories may be supplied to `report` to union actual events. Each directory must use the current identical source manifest and revision; changed sources require fresh collection. Release mutation tests use only local fake-gh/fake-curl fixtures and literal ordered endpoint/ordinal faults.
