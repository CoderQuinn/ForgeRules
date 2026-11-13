#!/usr/bin/env python3
"""Behavior tests and compiler-line coverage for the release coverage collector."""

import dis
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import trace
import types
import unittest
import uuid
from unittest import mock


ROOT = Path(__file__).resolve().parent.parent
COLLECTOR = ROOT / "Scripts/release_coverage.py"
HOOK_RUNS = []


class CollectorTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(COLLECTOR.is_file(), "syntax-based collector is missing")
        spec = importlib.util.spec_from_file_location("release_coverage", COLLECTOR)
        self.collector = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.collector)

    def test_literal_interiors_are_not_commands_and_continuations_are_owned(self):
        source = '''set -euo pipefail
value="$(
  printf ok
)"
jq '
  .not_a_bash_command
' \\
  input.json
if [[ "$value" == ok &&
      -n "$value" ]]; then
  printf '%s' "$value" > result
fi
'''
        sites = self.collector.command_sites(source.encode())
        owned = {line for site in sites for line in site["rows"]}
        self.assertEqual(owned, {1, 2, 3, 5, 8, 9, 10, 11})
        jq = next(site for site in sites if site["anchor"] == 5)
        self.assertEqual(jq["rows"], [5, 8])
        self.assertNotIn(6, owned)
        condition = next(site for site in sites if site["anchor"] == 9)
        self.assertEqual(condition["rows"], [9, 10])

    def test_substitution_sites_accept_only_their_syntax_at_runtime_closing_row(self):
        sites = self.collector.command_sites(b'v="$(\n printf hit\n false\n)"\n')
        nested = next(site for site in sites if site["anchor"] == 2)
        self.assertIn("fingerprint", nested)
        self.assertIn(4, nested["runtime_lines"])

    def test_identical_branch_commands_at_a_shared_runtime_row_stay_uncovered(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            source = root / "Scripts/probe.sh"
            source.write_text('v="$(\n if true; then\n  printf same\n else\n  printf same\n fi\n)"\n')
            fingerprint = self.collector.syntax_fingerprint(b"printf same")
            report = self.collector.summarize(root, self.collector.snapshot(root),
                                              {"Scripts/probe.sh": {(7, fingerprint)}}, {})
            self.assertTrue({3, 5}.issubset(report["files"][0]["uncovered"]))
            self.assertEqual(len(report["files"][0]["ambiguous_events"]), 1)

    def test_native_trace_preserves_failure_status_conditional_errexit_and_exit_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            (root / "Scripts/lib.sh").write_text('sourced() { printf sourced; }\n')
            (root / "Scripts/child.sh").write_text('printf child\n')
            probe = root / "Scripts/probe.sh"
            probe.write_text('''#!/usr/bin/env bash
set -euo pipefail
trap 'printf cleaned >"$RESULT/cleanup"' EXIT
source Scripts/lib.sh
sourced >"$RESULT/sourced"
status=0
false || status=$?
[[ "$status" == 1 ]]
conditional() { false; printf survived; }
if value="$(conditional)"; then
  [[ "$value" == survived ]]
else
  exit 9
fi
if value="$(exit 7)"; then
  exit 10
else
  [[ "$?" == 7 ]]
fi
value="$(
  printf nested
)"
[[ "$value" == nested &&
   -n "$value" ]]
bash "$FIXTURE_ROOT/Scripts/child.sh" >"$RESULT/child"
bash -c 'set -euo pipefail; f(){ false; printf preserved; }; if v="$(f)"; then [[ "$v" == preserved ]]; else exit 11; fi; printf direct' >"$RESULT/direct"
if false; then
  printf unexecuted
fi
''')
            probe.chmod(0o755)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "add", "Scripts"], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Fixture", "-c", "user.email=fixture@invalid.example", "commit", "-qm", "fixture"], check=True)
            environment = os.environ | {"FIXTURE_ROOT": str(root)}
            results = []
            for mode in ("plain", "traced"):
                output = root / mode
                output.mkdir()
                environment["RESULT"] = str(output)
                if mode == "plain":
                    result = subprocess.run(["bash", str(probe)], env=environment, cwd=root)
                    self.assertEqual(result.returncode, 0)
                else:
                    with mock.patch.dict(os.environ, environment):
                        self.collector.collect(root, root / "evidence", "Scripts/probe.sh", "HEAD")
                results.append({p.name: p.read_text() for p in output.iterdir()})
            self.assertEqual(results[0], {"sourced": "sourced", "child": "child", "direct": "direct", "cleanup": "cleaned"})
            self.assertEqual(results[0], results[1])
            head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            sources, hits, _ = self.collector.load_evidence(root, [root / "evidence"], head, head)
            report = self.collector.summarize(root, sources, hits, {})
            files = {f["path"]: f for f in report["files"]}
            self.assertEqual(files["Scripts/lib.sh"]["uncovered"], [])
            self.assertEqual(files["Scripts/child.sh"]["uncovered"], [])
            # Wrong-branch exits and the deliberately unexecuted printf stay uncovered.
            self.assertEqual(files["Scripts/probe.sh"]["uncovered"], [13, 16, 28])

    def test_parser_rejects_malformed_bash(self):
        with self.assertRaises(self.collector.CoverageError):
            self.collector.command_sites(b"if [[ x ]]; then\n")

    def test_missing_and_unexecuted_sources_fail_the_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            script = root / "Scripts/probe.sh"
            script.write_text("printf covered\nif false; then\n printf missed\nfi\n")
            (root / "Scripts/tool.py").write_text("value = 1\n")
            sources = self.collector.snapshot(root)
            sites = self.collector.command_sites(script.read_bytes())
            events = {(site["anchor"], site["fingerprint"]) for site in sites if site["anchor"] in {1, 2}}
            report = self.collector.summarize(root, sources, {"Scripts/probe.sh": events}, {})
            self.assertEqual(report["production"]["covered"], 2)
            self.assertEqual(report["production"]["total"], 3)
            self.assertEqual(report["files"][0]["uncovered"], [3])
            self.assertFalse(report["production"]["passed"])
            with self.assertRaisesRegex(self.collector.CoverageError, "missing"):
                self.collector.summarize(root, sources, {}, {})

    def test_stale_source_and_trace_evidence_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            script = root / "Scripts/probe.sh"
            script.write_text("printf original\n")
            sources = self.collector.snapshot(root)
            script.write_text("printf changed\n")
            with self.assertRaisesRegex(self.collector.CoverageError, "stale"):
                self.collector.summarize(root, sources, {"Scripts/probe.sh": {1}}, {})

    def test_empty_denominators_are_unmeasured(self):
        self.assertEqual(self.collector.fraction(set(), set()), {
            "covered": 0, "total": 0, "percent": None, "passed": False})
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            with self.assertRaisesRegex(self.collector.CoverageError, "missing Bash"):
                self.collector.summarize(root, {}, {}, {})

    def test_ansi_deparser_equivalence_and_unsupported_escape(self):
        for authored, deparsed in [(b"IFS=$'\\t' read x", b"IFS='\t' read x"),
                                   (b"x=$'\\n'", b"x='\n'"),
                                   (b"x=$'\\r'", b"x='\r'"),
                                   (b"x=$'\\\\'", b"x='\\'")]:
            self.assertEqual(self.collector.syntax_fingerprint(authored), self.collector.syntax_fingerprint(deparsed))
        with self.assertRaisesRegex(self.collector.CoverageError, "unsupported ANSI"):
            self.collector.syntax_fingerprint(b"x=$'\\e'")

    def test_default_stdout_descriptor_is_semantically_equivalent(self):
        for source in ("echo failure >&2", "echo failure 1>&2"):
            result = subprocess.run(["bash", "-c", source], capture_output=True, text=True, shell=False)
            self.assertEqual((result.returncode, result.stdout, result.stderr), (0, "", "failure\n"))
        self.assertEqual(self.collector.syntax_fingerprint(b"echo failure >&2"),
                         self.collector.syntax_fingerprint(b"echo failure 1>&2"))

    def test_compound_headers_arrays_and_redirections_have_explicit_ownership(self):
        sites = self.collector.command_sites(b'''declare -a data=(
 "one"
 "two"
)
for value in "${data[@]}"; do
 (( count += 1 ))
done
case "$value" in
 one) : ;;
esac
cat <(printf process) >out
''')
        owned = {row for site in sites for row in site["rows"]}
        self.assertTrue({1, 2, 3, 5, 6, 8, 9, 11}.issubset(owned))
        self.assertNotIn(4, owned)
        self.assertNotIn(7, owned)
        self.assertNotIn(10, owned)
        for text, kind in [(b"for a in b", "ForClause"), (b"case x in", "CaseClause"),
                           (b"time command", "TimeClause"), (b"coproc command", "CoprocClause")]:
            self.assertEqual(self.collector.event_fingerprint(text), "control:" + kind)
        with mock.patch.object(self.collector, "checked", side_effect=[b"v3.12.0", b'{"Cmd":{"Type":"FutureCommand"}}']):
            with self.assertRaisesRegex(self.collector.CoverageError, "unsupported Bash"):
                self.collector.command_sites(b":")
        with self.assertRaisesRegex(self.collector.CoverageError, "compound command redirection"):
            self.collector.command_sites(b"if true; then\n :\nfi > output\n")

    def test_entered_blocks_and_while_conditions_cover_only_owned_redirect_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            self.review_fixture(root, {"Scripts/main.sh": r'''set -euo pipefail
{
 printf '%s\n' header
 while IFS=$'\t' read -r name count; do
  printf '%s:%s\n' "$name" "$count"
 done < <(
  printf 'one\t1\n'
 )
 while false; do
  printf never
 done < /dev/null
} > \
 summary
printf after
'''})
            self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
            head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            sources, hits, _ = self.collector.load_evidence(root, [root / "evidence"], head, head)
            report = self.collector.summarize(root, sources, hits, {})
            self.assertEqual((root / "summary").read_text(), "header\none:1\n")
            self.assertEqual((root / "evidence/run.log").read_text(), "after")
            self.assertEqual(report["files"][0]["owned"], [1, 3, 4, 5, 6, 7, 9, 10, 11, 12, 13, 14])
            # Bash reports the producer at the outer statement's final row 13;
            # it has no approved alias there and remains conservatively uncovered.
            self.assertEqual(report["files"][0]["uncovered"], [7, 10])

    def test_skipped_and_failed_redirects_cannot_borrow_target_substitution_events(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            self.review_fixture(root, {"Scripts/main.sh": '''if false; then
 {
  printf skipped
 } > skipped
 while false; do
  printf skipped_loop
 done < skipped_input
fi
{
 printf forbidden_block
} > "$(
 printf absent
)/out" ||
 :
while printf forbidden_condition; do
 printf forbidden_body
done < "$(
 printf absent
)/input" ||
 :
printf survived
'''})
            self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
            head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            sources, hits, _ = self.collector.load_evidence(root, [root / "evidence"], head, head)
            report = self.collector.summarize(root, sources, hits, {})
            self.assertEqual(report["files"][0]["covered_rows"], [1, 14, 20, 21])
            self.assertEqual(report["files"][0]["uncovered"], [3, 4, 5, 6, 7, 10, 11, 12, 15, 16, 17, 18])
            self.assertFalse((root / "skipped").exists())
            self.assertNotIn("forbidden", (root / "evidence/run.log").read_text())

    def test_matched_target_substitution_cannot_prove_its_own_container_redirect(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            (root / "Scripts/probe.sh").write_text('''{
 printf forbidden
} > "$(
 printf absent
)/out"
''')
            event = (4, self.collector.syntax_fingerprint(b"printf absent"))
            report = self.collector.summarize(root, self.collector.snapshot(root),
                                              {"Scripts/probe.sh": {event}}, {})
            self.assertEqual(report["files"][0]["covered_rows"], [4])
            self.assertEqual(report["files"][0]["uncovered"], [2, 3])

    def test_deferred_function_body_does_not_prove_its_declaration_block_redirect(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            self.review_fixture(root, {"Scripts/main.sh": '''{
 later() {
  {
   printf called
  } > called
 }
} > declaration
later
'''})
            self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
            head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            sources, hits, _ = self.collector.load_evidence(root, [root / "evidence"], head, head)
            report = self.collector.summarize(root, sources, hits, {})
            self.assertEqual((root / "called").read_text(), "called")
            self.assertEqual((root / "declaration").read_text(), "")
            self.assertEqual(report["files"][0]["covered_rows"], [4, 5, 8])
            self.assertEqual(report["files"][0]["uncovered"], [7])

    def test_ambiguous_descendants_do_not_credit_enclosing_redirects(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            (root / "Scripts/probe.sh").write_text('''v="$(
 if true; then
  {
   printf same
  } > first
 else
  {
   printf same
  } > second
 fi
)"
''')
            event = (11, self.collector.syntax_fingerprint(b"printf same"))
            report = self.collector.summarize(root, self.collector.snapshot(root),
                                              {"Scripts/probe.sh": {event}}, {})
            self.assertEqual(report["files"][0]["covered_rows"], [])
            self.assertEqual(report["files"][0]["uncovered"], [1, 2, 4, 5, 8, 9])
            self.assertEqual(report["files"][0]["ambiguous_events"][0]["candidate_anchors"], [4, 8])

    def test_changed_row_parser_includes_continuations_and_zero_count_hunks(self):
        diff = b'''diff --git a/Scripts/a.sh b/Scripts/a.sh
+++ b/Scripts/a.sh
@@ -1 +1,3 @@
@@ -8,2 +10,0 @@
+++ b/Scripts/other.sh
@@ -0,0 +1 @@
'''
        with mock.patch.object(self.collector, "resolve_commit", return_value="base"), mock.patch.object(self.collector, "checked", return_value=diff):
            self.assertEqual(self.collector.changed_rows(ROOT, "base"), {
                "Scripts/a.sh": {1, 2, 3}, "Scripts/other.sh": {1}})

    def test_versions_and_process_failures_do_not_look_supported(self):
        with self.assertRaisesRegex(self.collector.CoverageError, "command failed"):
            self.collector.checked(["/bin/sh", "-c", "echo failed >&2; exit 3"])
        with mock.patch.object(self.collector, "checked", return_value=b"v3.12.01\n"):
            with self.assertRaisesRegex(self.collector.CoverageError, "unsupported"):
                self.collector.tool("shfmt", "v3.12.0")

    def evidence_fixture(self, root):
        (root / "Scripts").mkdir()
        (root / "Scripts/probe.sh").write_text("printf covered\n")
        directory = root / "evidence"
        directory.mkdir()
        fingerprint = self.collector.syntax_fingerprint(b"printf covered")
        (directory / "events.json").write_text(json.dumps({"events": [["Scripts/probe.sh", 1, fingerprint, self.collector.digest(root / "Scripts/probe.sh"), "5.3.15(1)", "", ""]]}))
        (directory / "trace-environment.sh").write_text(":\n")
        (directory / "trace-success").write_bytes(b"fixture-latch")
        (directory / "write-failures").write_text("")
        record = {"sources": self.collector.snapshot(root), "base": "base", "head": "head", "exit": 0,
                  "started_ns": 1, "finished_ns": 2, "parser": "v3.12.0",
                  "bash": "GNU bash, version 5.3.15(1)", "trace_format": 4,
                  "events_sha256": self.collector.digest(directory / "events.json"),
                  "helper_sha256": self.collector.digest(directory / "trace-environment.sh"),
                  "success_latch": {"identity": self.collector.file_identity(directory / "trace-success"),
                                    "sha256": self.collector.digest(directory / "trace-success")},
                  "failure_sha256": self.collector.digest(directory / "write-failures")}
        (directory / "evidence.json").write_text(json.dumps(record))
        return directory, record

    def test_trace_provenance_negative_cases(self):
        for change in [{"base": "other"}, {"head": "other"}, {"sources": {}}, {"exit": 1},
                       {"finished_ns": 0}, {"parser": "future"}, {"bash": "old"},
                       {"trace_format": 3}, {"trace_format": True}, {"bash": "GNU bash, version 5.3.150(1)"},
                       {"events_sha256": "stale"}, {"helper_sha256": "stale"}]:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                directory, record = self.evidence_fixture(root)
                record.update(change)
                (directory / "evidence.json").write_text(json.dumps(record))
                with self.assertRaises(self.collector.CoverageError):
                    self.collector.load_evidence(root, [directory], "base", "head")
        with self.assertRaisesRegex(self.collector.CoverageError, "missing trace"):
            self.collector.load_evidence(ROOT, [], "base", "head")

    def test_malformed_native_event_fields_fail_closed(self):
        for event in [["foreign.sh", 1, "a" * 64], ["Scripts/probe.sh", 0, "a" * 64],
                      ["Scripts/probe.sh", True, "a" * 64], ["Scripts/probe.sh", 1, "bad"]]:
            with self.subTest(event=event), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                directory, record = self.evidence_fixture(root)
                (directory / "events.json").write_text(json.dumps({"events": [event + [self.collector.digest(root / "Scripts/probe.sh"), "5.3.15(1)", "", ""]]}))
                record["events_sha256"] = self.collector.digest(directory / "events.json")
                (directory / "evidence.json").write_text(json.dumps(record))
                with self.assertRaisesRegex(self.collector.CoverageError, "malformed|stale"):
                    self.collector.load_evidence(root, [directory], "base", "head")

    def test_temporary_event_validation_and_cloned_source_identity(self):
        for mode in ("clone", "relative", "truncated", "negative", "external", "empty"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                (root / "Scripts").mkdir()
                source = root / "Scripts/probe.sh"
                source.write_text("printf covered\n")
                directory = root / "evidence"
                (directory / "temporary-events").mkdir(parents=True)
                name = str(source) if mode != "relative" else "Scripts/probe.sh"
                sha = self.collector.digest(source)
                if mode in ("clone", "external"):
                    name = "/removed/synthetic/clone"
                    sha = self.collector.digest(source) if mode == "clone" else "unmatched"
                data = b"\0".join([name.encode(), b"-1" if mode == "negative" else b"1",
                                     b"printf covered", sha.encode(), b"5.3.15(1)", b"", b""]) + b"\0"
                if mode == "truncated": data = data[:-1]
                if mode == "empty": data = b""
                (directory / "temporary-events/1.events").write_bytes(data)
                if mode in ("clone", "relative"):
                    result = self.collector.finalize_events(root, directory, self.collector.snapshot(root))
                    self.assertFalse((directory / "temporary-events").exists())
                    event = json.loads(result.read_text())["events"][0]
                    self.assertEqual(event[:2], ["Scripts/probe.sh", 1])
                    self.assertNotIn("printf covered", result.read_text())
                else:
                    with self.assertRaises(self.collector.CoverageError):
                        self.collector.finalize_events(root, directory, self.collector.snapshot(root))

    def test_cli_report_passes_only_measured_gates_and_rejects_absent_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            directory, _ = self.evidence_fixture(root)
            arguments = ["report", str(directory), "--root", str(root), "--base", "base"]
            hook_evidence = root / "hook.json"
            hook_evidence.write_text("{}")
            arguments += ["--hook-evidence", str(hook_evidence)]
            original_checked = self.collector.checked
            def checked(command, **kwargs):
                return b"head\n" if command[:3] == ["git", "rev-parse", "HEAD"] else original_checked(command, **kwargs)
            with mock.patch.object(self.collector, "resolve_commit", return_value="base"), \
                    mock.patch.object(self.collector, "checked", side_effect=checked), \
                    mock.patch.object(self.collector, "hook_coverage", return_value={"passed": True}), \
                    mock.patch.object(self.collector, "changed_rows", return_value={"Scripts/probe.sh": {1}}), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                self.assertEqual(self.collector.main(arguments), 0)
                self.assertEqual(json.loads(output.getvalue())["production"]["covered"], 1)
            with mock.patch.object(self.collector, "resolve_commit", return_value="base"), \
                    mock.patch.object(self.collector, "checked", side_effect=checked), \
                    mock.patch.object(self.collector, "hook_coverage", return_value={"passed": True}), \
                    mock.patch.object(self.collector, "changed_rows", return_value={}), \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(self.collector.main(arguments), 1)
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(self.collector.main(["collect", "one", "two"]), 2)
            with mock.patch.object(self.collector, "collect") as collect:
                self.assertEqual(self.collector.main(["collect", "one"]), 0)
                collect.assert_called_once()

    def test_failed_runs_and_sources_changed_during_execution_are_rejected(self):
        for mode in ("failure", "source-change"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / "Scripts").mkdir()
                script = root / "Scripts/probe.sh"
                script.write_text("exit 7\n" if mode == "failure" else "printf ':\\n' > Scripts/probe.sh\n")
                original_checked = self.collector.checked
                def checked(command, **kwargs):
                    return b"head\n" if command[:3] == ["git", "rev-parse", "HEAD"] else original_checked(command, **kwargs)
                with mock.patch.object(self.collector, "resolve_commit", return_value="base"), mock.patch.object(self.collector, "checked", side_effect=checked):
                    with self.assertRaisesRegex(self.collector.CoverageError, "trace command failed|source changed"):
                        self.collector.collect(root, root / "evidence", "Scripts/probe.sh", "base")

    def review_fixture(self, root, files):
        (root / "Scripts").mkdir()
        for name, body in files.items():
            (root / name).write_text(body)
            (root / name).chmod(0o755)
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(["git", "-C", str(root), "add", "Scripts"], check=True)
        subprocess.run(["git", "-C", str(root), "-c", "user.name=Fixture", "-c", "user.email=fixture@invalid.example", "commit", "-qm", "fixture"], check=True)

    def test_external_source_cannot_borrow_the_main_script_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve() / "repository"
            root.mkdir()
            self.review_fixture(root, {"Scripts/main.sh": 'if false; then\n printf wrongly_credited\nfi\nsource "$FIXTURE_EXTERNAL"\n'})
            external = Path(tmp) / "external.sh"
            external.write_text('\nprintf wrongly_credited\n')
            with mock.patch.dict(os.environ, {"FIXTURE_EXTERNAL": str(external)}):
                self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
            head = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            sources, hits, _ = self.collector.load_evidence(root, [root / "evidence"], head, head)
            result = self.collector.summarize(root, sources, hits, {})
            self.assertIn(2, result["files"][0]["uncovered"])
            events = json.loads((root / "evidence/events.json").read_text())
            self.assertIn(str(external.resolve()), events["external_sources"])

    def test_transient_mutation_is_rejected_even_after_an_earlier_source(self):
        for earlier_source in (False, True):
            with self.subTest(earlier_source=earlier_source), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                prefix = "source Scripts/lib.sh\n" if earlier_source else ""
                self.review_fixture(root, {"Scripts/main.sh": prefix + '''cp Scripts/lib.sh saved
printf 'if true; then\n printf wrongly_credited\nfi\n' > Scripts/lib.sh
source Scripts/lib.sh
mv saved Scripts/lib.sh
''', "Scripts/lib.sh": 'if false; then\n printf wrongly_credited\nfi\n'})
                with self.assertRaisesRegex(self.collector.CoverageError, "source.*digest|stale"):
                    self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
                self.assertEqual((root / "Scripts/lib.sh").read_text(), 'if false; then\n printf wrongly_credited\nfi\n')

    def test_unsupported_child_interpreter_never_earns_coverage(self):
        if "5.3.15(" in subprocess.check_output(["/bin/bash", "--version"], text=True):
            self.skipTest("system Bash is the pinned interpreter")
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            self.review_fixture(root, {"Scripts/main.sh": "Scripts/child.sh\n", "Scripts/child.sh": '#!/bin/bash\nprintf child\n'})
            with self.assertRaisesRegex(self.collector.CoverageError, "unsupported.*Bash"):
                self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
            self.assertEqual((root / "evidence/run.log").read_text(), "child")

    def test_unexecuted_redirect_only_statement_remains_in_denominator(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            (root / "Scripts/probe.sh").write_text('if false; then\n > never-created\nfi\n')
            event = (1, self.collector.syntax_fingerprint(b"false"))
            report = self.collector.summarize(root, self.collector.snapshot(root), {"Scripts/probe.sh": {event}}, {})
            self.assertEqual(report["production"]["total"], 2)
            self.assertEqual(report["files"][0]["uncovered"], [2])

    def test_cached_function_from_mutated_source_is_rejected_after_restore(self):
        for replacement in ('f() {\n if true; then\n  printf wrongly_credited\n fi\n}\n',
                            'f() {\n :\n  printf wrongly_credited\n}\n'):
            with self.subTest(replacement=replacement), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                main = 'cp Scripts/lib.sh saved\nprintf %s "$REPLACEMENT" > Scripts/lib.sh\nsource Scripts/lib.sh\nmv saved Scripts/lib.sh\nf\n'
                self.review_fixture(root, {"Scripts/main.sh": main,
                                          "Scripts/lib.sh": 'f() {\n if false; then\n  printf wrongly_credited\n fi\n}\n'})
                with mock.patch.dict(os.environ, {"REPLACEMENT": replacement}):
                    with self.assertRaisesRegex(self.collector.CoverageError, "function.*definition|source"):
                        self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")

    def test_invalid_base_is_rejected_before_artifact_creation(self):
        for base in ("--output=forged-diff", "not-a-commit"):
            with self.subTest(base=base), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                self.review_fixture(root, {"Scripts/main.sh": "printf safe\n"})
                with contextlib.redirect_stderr(io.StringIO()):
                    code = self.collector.main(["collect", str(root / "evidence"), "--root", str(root),
                                                "--entry", "Scripts/main.sh", "--base=" + base])
                self.assertEqual(code, 2)
                self.assertFalse((root / "evidence").exists())
                self.assertFalse((root / "forged-diff").exists())

    def test_specific_enclosing_function_resolves_shared_runtime_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "Scripts").mkdir()
            body = b'x="$(\n foo(){\n  printf same\n }\n bar(){\n  printf same\n }\n foo\n)"\n'
            (root / "Scripts/probe.sh").write_bytes(body)
            definitions = self.collector.function_definitions(body)
            event = (9, self.collector.syntax_fingerprint(b"printf same"), "foo", definitions["foo"][0])
            result = self.collector.summarize(root, self.collector.snapshot(root), {"Scripts/probe.sh": {event}}, {})
            self.assertIn(3, result["files"][0]["covered_rows"])
            self.assertIn(6, result["files"][0]["uncovered"])

    def test_ambiguous_same_name_function_definitions_fail_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            self.review_fixture(root, {"Scripts/main.sh": 'f(){ printf first; }\nf(){ printf second; }\nf\n'})
            with self.assertRaisesRegex(self.collector.CoverageError, "ambiguous runtime function"):
                self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")

    def test_authored_hook_is_a_first_class_source(self):
        hook = ROOT / "Scripts/coverage/release-trace-hook.sh"
        self.assertTrue(hook.is_file(), "authored Bash hook is hidden inside a Python literal")
        source = hook.read_bytes()
        owned = {row for site in self.collector.command_sites(source) for row in site["rows"]}
        self.assertGreater(len(owned), 20)
        self.assertIn(b"_forgerules_emit_debug", source)

    def test_absent_independent_hook_trace_fails_closed(self):
        self.assertTrue(hasattr(self.collector, "hook_coverage"), "independent Bash hook coverage is missing")
        with self.assertRaisesRegex(self.collector.CoverageError, "missing"):
            self.collector.hook_coverage({"runs": []})

    def test_logged_hook_write_failure_rejects_prior_complete_events(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            (root / "Scripts").mkdir()
            source = root / "Scripts/probe.sh"
            source.write_text("printf covered\n")
            evidence = root / "evidence"
            (evidence / "temporary-events").mkdir(parents=True)
            event = b"\0".join([str(source).encode(), b"1", b"printf covered", self.collector.digest(source).encode(),
                                  b"5.3.15(1)", b"", b""]) + b"\0"
            (evidence / "temporary-events/1.events").write_bytes(event)
            (evidence / "write-failures").write_text("1\n")
            with self.assertRaisesRegex(self.collector.CoverageError, "incomplete.*write"):
                self.collector.finalize_events(root, evidence, self.collector.snapshot(root))
            self.assertFalse((evidence / "events.json").exists())

    def test_event_and_marker_append_failures_preserve_target_but_never_finalize(self):
        for mode in ("event-only", "event-and-marker", "restored-permissions"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve()
                targets = '"$FORGERULES_COVERAGE_EVENTS/$BASHPID.events"'
                if mode != "event-only": targets += ' "$FORGERULES_COVERAGE_FAILURE"'
                body = 'set -euo pipefail\ntrap \'printf "%s" "$?" > "$TARGET_STATUS"\' EXIT\nprintf before\nchmod a-w ' + targets + '\nprintf after\n'
                if mode == "restored-permissions": body += 'chmod u+w ' + targets + '\n'
                self.review_fixture(root, {"Scripts/main.sh": body})
                plain = root / "plain"
                plain.mkdir()
                environment = os.environ.copy()
                environment.pop("BASH_ENV", None)
                environment.update({"FORGERULES_COVERAGE_EVENTS": str(plain),
                                    "FORGERULES_COVERAGE_FAILURE": str(plain / "write-failures"),
                                    "TARGET_STATUS": str(plain / "status")})
                setup = ': > "$FORGERULES_COVERAGE_EVENTS/$BASHPID.events"\n: > "$FORGERULES_COVERAGE_FAILURE"\n'
                result = subprocess.run(["/opt/homebrew/bin/bash", "-c", setup + body], env=environment,
                                        shell=False, capture_output=True, text=True)
                self.assertEqual((result.returncode, result.stdout, result.stderr), (0, "beforeafter", ""))
                with mock.patch.dict(os.environ, {"TARGET_STATUS": str(root / "traced-status")}):
                    with self.assertRaisesRegex(self.collector.CoverageError, "incomplete|write failure"):
                        self.collector.collect(root, root / "evidence", "Scripts/main.sh", "HEAD")
                self.assertEqual((root / "evidence/run.log").read_text(), result.stdout)
                self.assertEqual((root / "traced-status").read_text(), (plain / "status").read_text())
                self.assertFalse((root / "evidence/events.json").exists())

    def run_hook_meta(self, hook, program, label, bash="/opt/homebrew/bin/bash", hasher="/usr/bin/openssl", virtual=False):
        artifact = Path(tempfile.mkdtemp(prefix="forgerules-hook-meta-"))
        events = artifact / "events"
        if label != "log-failure": events.mkdir()
        latch = artifact / "trace-success"
        latch.write_bytes(b"independent-fixture-latch")
        nonce = uuid.uuid4().hex[:16]
        driver = artifact / "driver.sh"
        driver.write_text('PS4="$FORGE_META_PS4"\nsource "$FORGE_HOOK"\nset -euo pipefail\n' + program + "\n")
        raw = artifact / "xtrace.log"
        before = self.collector.file_identity(hook)
        environment = os.environ.copy()
        environment.pop("BASH_ENV", None)
        environment.pop("BASH_XTRACEFD", None)
        environment.update({"FORGERULES_COVERAGE_EVENTS": str(events), "FORGERULES_COVERAGE_HASHER": hasher,
                            "FORGERULES_COVERAGE_FAILURE": str(artifact / "write-failures"), "FORGERULES_COVERAGE_LATCH": str(latch),
                            "FORGE_META_PS4": "+FORGE_META_" + nonce + "\t${BASH_VERSION}\t${BASHPID:-$$}\t${BASH_SOURCE[0]:-$0}\t${LINENO}\t"})
        version = subprocess.check_output([bash, "-c", 'printf "%s" "$BASH_VERSION"'], env=environment, text=True)
        results = []
        for mode in ("plain", "traced"):
            output = artifact / mode
            output.mkdir()
            hook_argument = "./" + hook.name if version.startswith("3.") else str(hook)
            environment.update({"RESULT": str(output), "FORGE_HOOK": hook_argument if mode == "traced" else "/dev/null"})
            argv = [bash]
            if mode == "traced": argv.append("-x")
            argv += ["-c", driver.read_text(), "virtual-hook-probe"] if virtual else [str(driver)]
            with raw.open("wb" if mode == "traced" else "ab") as trace_file:
                if mode == "traced" and version.startswith("5."):
                    environment["BASH_XTRACEFD"] = str(trace_file.fileno())
                else:
                    environment.pop("BASH_XTRACEFD", None)
                result = subprocess.run(argv, env=environment, cwd=hook.parent, shell=False, stdout=subprocess.PIPE,
                                        stderr=trace_file if mode == "traced" and version.startswith("3.") else subprocess.PIPE,
                                        pass_fds=(trace_file.fileno(),), timeout=30)
            stderr = result.stderr
            if stderr is None:
                stderr = b"\n".join(line for line in raw.read_bytes().splitlines() if not line.startswith(b"+"))
            results.append((result.returncode, result.stdout, stderr,
                            {p.name: p.read_bytes() for p in output.iterdir()}))
        self.assertEqual(results[0], results[1], label)
        self.assertEqual(results[1][0], 0, label)
        with raw.open("ab") as output:
            output.write(("FORGE_META_COMPLETE_" + nonce + "\n").encode())
        record = {"source_path": str(hook.resolve()), "source_sha256": self.collector.digest(hook),
                  "before": before, "after": self.collector.file_identity(hook), "parser": "v3.12.0",
                  "bash": version, "nonce": nonce, "trace_path": str(raw), "trace_sha256": self.collector.digest(raw),
                  "exit": result.returncode, "parity": True, "profile": label, "hasher": hasher, "cwd": str(hook.parent),
                  "xtrace_channel": "BASH_XTRACEFD" if version.startswith("5.") else "Bash3 stderr negative fixture"}
        if label == "log-failure":
            self.assertTrue((artifact / "write-failures").read_text().strip())
            self.assertFalse(latch.exists())
        return record

    def test_independent_hook_profiles_preserve_program_effects(self):
        hook = self.collector.HOOK
        normal = '''status=0
false || status=$?
[[ "$status" == 1 ]]
f(){ false; printf survived; }
for attempt in 1 2; do
 if value="$(f)"; then [[ "$value" == survived ]]; else exit 9; fi
done
if value="$(exit 7)"; then exit 10; else [[ "$?" == 7 ]]; fi
cleanup(){ printf closed >"$RESULT/cleanup"; }
trap cleanup EXIT
printf 'printf library >/dev/null\\n' >"$RESULT/library.sh"
source "$RESULT/library.sh"
printf product'''
        cases = [("normal", normal, "/usr/bin/openssl", False),
                 ("hash-failure", normal, "/usr/bin/false", False),
                 ("virtual", "printf product", "/usr/bin/openssl", True),
                 ("missing-function", "f(){ unset -f f; printf product; }; f", "/usr/bin/openssl", False),
                 ("log-failure", "printf product", "/usr/bin/openssl", False)]
        for label, program, hasher, virtual in cases:
            with self.subTest(label=label):
                record = self.run_hook_meta(hook, program, label, hasher=hasher, virtual=virtual)
                HOOK_RUNS.append(record)
        if "5.3.15(" not in subprocess.check_output(["/bin/bash", "--version"], text=True):
            HOOK_RUNS.append(self.run_hook_meta(hook, "printf product", "unsupported-child", bash="/bin/bash"))
        result = self.collector.hook_coverage({"runs": HOOK_RUNS, "tests_passed": True})
        self.assertTrue(result["passed"], result["uncovered"])

    def test_hook_metric_rejects_unexecuted_and_bad_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.sh"
            probe.write_text('printf observed >/dev/null\nif false; then\n printf missing\nfi\n')
            record = self.run_hook_meta(probe, "printf product", "unexecuted-probe")
            result = self.collector.hook_coverage({"runs": [record], "tests_passed": True}, hook=probe)
            self.assertEqual((result["covered"], result["total"], result["uncovered"]), (2, 3, [3]))
            self.assertFalse(result["passed"])
            for mutation in [{"source_sha256": "stale"}, {"before": {}}, {"parser": "future"},
                             {"parity": False}, {"bash": "future"}, {"nonce": "invalid"}, {"trace_sha256": "stale"}]:
                with self.subTest(mutation=mutation), self.assertRaises(self.collector.CoverageError):
                    self.collector.hook_coverage({"runs": [record | mutation], "tests_passed": True}, hook=probe)
            trace_file = Path(record["trace_path"])
            trace_file.write_bytes(trace_file.read_bytes().split(b"FORGE_META_COMPLETE_")[0])
            record["trace_sha256"] = self.collector.digest(trace_file)
            with self.assertRaisesRegex(self.collector.CoverageError, "truncated"):
                self.collector.hook_coverage({"runs": [record], "tests_passed": True}, hook=probe)


def executable_lines(path):
    """Use CPython compiler line tables, including nested function code."""
    def visit(code):
        lines = {line for _, line in dis.findlinestarts(code) if line > 0}
        for child in code.co_consts:
            if isinstance(child, types.CodeType):
                lines.update(visit(child))
        return lines
    return visit(compile(path.read_bytes(), str(path), "exec"))


if __name__ == "__main__":
    runner = trace.Trace(count=True, trace=False)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(CollectorTests)
    result = runner.runfunc(unittest.TextTestRunner(verbosity=2).run, suite)
    collector_passed = True
    if os.environ.get("FORGERULES_PYTHON_COVERAGE") and COLLECTOR.exists():
        counts = runner.results().counts
        files = {}
        for path in (COLLECTOR, Path(__file__).resolve()):
            owned = executable_lines(path)
            covered = {line for (name, line), hits in counts.items()
                       if Path(name).resolve() == path and hits > 0}
            files[str(path.relative_to(ROOT))] = {
                "sha256": __import__("hashlib").sha256(path.read_bytes()).hexdigest(),
                "owned": sorted(owned), "covered": sorted(owned & covered),
                "uncovered": sorted(owned - covered),
            }
        collector = files["Scripts/release_coverage.py"]
        collector["percent"] = 100 * len(collector["covered"]) / len(collector["owned"])
        collector_passed = collector["percent"] >= 95
        Path(os.environ["FORGERULES_PYTHON_COVERAGE"]).write_text(json.dumps({
            "python": sys.version, "success": result.wasSuccessful(), "files": files,
            "collector_passed": collector_passed,
        }, indent=2) + "\n")
    sys.exit(0 if result.wasSuccessful() and collector_passed else 1)
