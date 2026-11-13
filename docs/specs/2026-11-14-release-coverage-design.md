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

An executable command owns unique physical rows on which its AST command token, argument/assignment/array element, test operand/operator or redirection starts. A multiline argument's quoted interior is data, not additional Bash rows. Simple commands, declarations, tests, arithmetic, pure redirect-only statements and evaluated for/case/time/coproc headers are counted. Compound-container redirections are currently rejected as unsupported, rather than silently omitting their executable targets. Function declarations, container delimiters and comments add no independent rows; their executable child commands still count. The `go list` and `go test` command owners include the added `internal/...` continuation arguments. This is command-source-row coverage, not branch coverage or jq/Python-in-string coverage.

Each native event records the actual `BASH_SOURCE` identity, its freshly calculated SHA-256, the actual child Bash version, runtime row and unexpanded command syntax temporarily. Source digests are calculated per event without a file-identity cache, including repeated sourcing. System OpenSSL performs the runtime SHA-256 and its version is recorded; Python independently checks each digest against the source manifest. A virtual `bash -c` location never borrows `$0` as a source identity. Pinned shfmt normalization plus position-free AST hashing yields the retained syntax fingerprint; temporary syntax text is removed after finalization. Runtime rows must match the command's start/end or an enclosing command/process substitution's closing row. Identical commands that remain ambiguous at a shared runtime row earn no coverage and are reported explicitly. Bash can retain a stale `BASH_COMMAND` inside EXIT handlers; unmatched handler rows remain conservatively uncovered, never credited by guessing a whole span. The denominator retains them.

All `Scripts/**/*.sh` files are enumerated: operational files are production; `test-*` and `fixtures/` are separately reported support. No production file or uncovered row is excluded. Source SHA-256 manifests include shell and Python tooling/support; raw event and trace-helper hashes, exact base/head, versions, exit status and run timestamps bind each collection. A copied synthetic fixture is mapped to its authored source only by its runtime-recorded complete-file digest; unknown external sources are reported. All in-repository and cloned-source event digests must match, and every event must attest Bash 5.3.15. Unsupported absolute-shebang children run without the unsupported tracer and cause the entire collection to fail explicitly. Missing, malformed, changed-during-run and stale evidence fails closed. Empty denominators are N/A (`null`), never measured 100%.

The collector's Python executable rows use CPython compiler line tables recursively across nested code objects, with actual stdlib `trace` counters from its tests. Its dedicated gate requires 95%; its tests and the release-fault Python suite are reported separately with all uncovered rows. This is line coverage, not branch coverage.

## Reproduction commands

Put the pinned Bash and shfmt binaries on `PATH`, or provide `SHFMT` for the parser. No global shell/profile change is required.

```bash
FORGERULES_PYTHON_COVERAGE=/tmp/collector-coverage.json \
FORGERULES_HOOK_COVERAGE=/tmp/hook-coverage.json \
python3 Scripts/test_release_coverage.py
python3 Scripts/release_coverage.py collect /tmp/release-trace --entry Scripts/test-verify-release-assets.sh
python3 Scripts/release_coverage.py report /tmp/release-trace \
  --hook-evidence /tmp/hook-coverage.json > /tmp/release-coverage.json
```

The report command enforces the production and added-production Bash thresholds. It must be paired with the collector test/coverage command above; a Bash percentage cannot replace the collector's separate gate. Multiple `collect` directories may be supplied to `report` to union actual events. Each directory must use the current identical source manifest and revision; changed sources require fresh collection. Release mutation tests use only local fake-gh/fake-curl fixtures and literal ordered endpoint/ordinal faults.

## Collector review corrections

Independent review of the first checkpoint reproduced three false-positive risks: `$0` hashes misattributed an external sourced file; unchecked in-repository event identity accepted transient source mutation; parent-only version attestation accepted a Bash 3.2 child. It also found an omitted redirect-only denominator row. All were reproduced as failing tests before correction, including a repeated-source mutation case. Prior checkpoint coverage percentages are diagnostic and do not qualify release orchestration. Trace format 2 retains and checks source SHA-256 and actual Bash version in every event; all final evidence must be freshly collected with the corrected tool.

## Loaded functions and revision identity

Current disk bytes alone do not identify a previously loaded function. Every known-source function event also records an interned runtime `declare -f` definition. The collector validates its normalized definition against the approved source and binds a credited site to its specific enclosing `FuncDecl`. Multiple same-name definitions in one source are rejected as ambiguous. This rejects definition-only transient loading followed by file restoration, including changed or removed guards. Repeated raw definition text is interned by SHA-256 before finalization; accepted events retain the normalized definition identity. This is bounded local evidence, not host tamper attestation.

Trace format 3 requires this function context in addition to source SHA and actual Bash version. Bases are resolved option-safely to one canonical commit OID before collection or diff calculation. Flags and noncommits fail before an evidence directory is created. Bash's equivalent default `>&2` and explicit `1>&2` spelling is normalized only after a fixture proves identical process output and exit behavior; no release condition or identity check changes.

The authored hook remains a separate Bash collector scope; a passing Python collector percentage does not close its independent gate.

### Bash function deparsing and guarded START aliases

Bash `declare -f` expands an authored `elif` into `else` containing a nested `if`. Canonicalization treats these spellings equally only when the else wrapper has no condition and exactly one foreground, unnegated, unredirected `IfClause` statement. Conditions, body order, additional statements, redirects and pipeline structure remain in the fingerprint. Actual Bash fixtures compare all three outputs and nonzero statuses, validate the resulting runtime function binding, and reject altered guards and statement forms. A pinned-minifier background form that cannot be parsed remains unsupported rather than earning credit.

Pinned Bash can report a simple substitution producer at its owning statement's starting row. That alias is eligible only for a single direct call/declaration body in the same approved function, with independent direct evidence of the owner and globally unambiguous syntax within that source/function. Skipped or failed substitutions, deferred functions and stale EXIT syntax cannot borrow it. Complex enclosing-container END aliases remain unsupported and their owned rows remain uncovered. These analyzer rules change no hook bytes, event format or release behavior.

## First-class Bash hook checkpoint

The executable hook is authored in `Scripts/coverage/release-trace-hook.sh`; the collector copies its exact bytes to the evidence helper. It uses a named Bash callback so its body is visible to the syntax parser. It is collector tooling, separately inventoried with a mandatory Bash coverage gate; it is never mixed into release-operational production or test-support totals. All provenance and status-parity tests remain required. Independent hook xtrace measurement remains open until its dedicated fixture/evidence implementation is complete.

## Independent hook evidence and redirected containers

Hook execution is now measured independently through Bash xtrace on a dedicated `BASH_XTRACEFD`, never from its own DEBUG events. The synthetic profiles compare plain/traced exit status, stdout, stderr and side effects for status/errexit/EXIT, missing or virtual source, failed hash dependency, removed function definition and event-log failure. A separately labeled real Bash 3.2 negative fixture measures the unsupported-interpreter guard; it is not relabeled as Bash 5 production evidence. Short relative source framing avoids Bash 3's long-PS4 truncation. Raw trace hashes, exact source bytes/identity, versions, process/source/row framing and completion markers bind the measurements. Missing, stale, unexecuted and truncated trace probes fail the gate.

Instrumentation write failures preserve the program's effects but record a separate failure flag, which rejects even otherwise complete prior DEBUG events. The report requires independent hook evidence via `--hook-evidence`; the hook receives its own mandatory tooling numerator/denominator and never inflates release production.

Reviewed block and while-container redirect support includes each executable redirect target in the denominator. Only an unambiguous executed descendant inside that container can establish redirect entry. Target substitutions cannot prove their own redirect succeeded, and later function execution cannot prove a declaration block was entered. Unsupported container types remain explicit errors. These checks are collector compatibility work and do not change any release or consumer script.

## Bounded failure latch

A marker append can itself fail. Collection now precreates an unpredictable regular-file success latch and keeps its exact bytes and file identity in collector memory. Every event, definition or unsupported-version write failure unlinks that latch before attempting the auxiliary failure append. The callback still returns zero so target status and effects are preserved. Finalization and later evidence loading require the original latch identity/bytes and empty hashed failure state. Prior valid events cannot rescue a missing or changed latch. Format 4 binds these controls; evidence directories are fresh and failed final writes are never reused as a successful result.

The bounded guarantee covers ordinary append failures while the collector-owned evidence parent remains writable, including both event and marker files becoming read-only and later restoring their permissions. Permanent missing, read-only or full evidence storage fails required reads/final writes. A transient outage that simultaneously prevents append and latch unlink, then recovers without an observable control change before finalization, is not attested by this design. This is not arbitrary host or kernel tamper attestation.

## Static process-boundary audit

The collector and local fixtures use argv-only subprocess calls. The developer intentionally selects the repository entry and trusted parser/interpreter/hash tools; fixture commands are fixed local git verbs or authored synthetic Bash programs. The source carries narrowly scoped audit annotations only at actual Bandit B404/B603/B607 or exact Semgrep subprocess-audit matches, with adjacent trust rationale. The tainted-environment rule remains enabled, and no scanner configuration, source exclusion or repository security setting is weakened. Annotation-only changes retain identical Python ASTs; they require refreshed byte-hash provenance before final measurements.
