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
from unittest import mock


ROOT = Path(__file__).resolve().parent.parent
COLLECTOR = ROOT / "Scripts/release_coverage.py"


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
            self.collector.command_sites(b"{\n :\n} > output\n")

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
        record = {"sources": self.collector.snapshot(root), "base": "base", "head": "head", "exit": 0,
                  "started_ns": 1, "finished_ns": 2, "parser": "v3.12.0",
                  "bash": "GNU bash, version 5.3.15(1)", "trace_format": 3,
                  "events_sha256": self.collector.digest(directory / "events.json"),
                  "helper_sha256": self.collector.digest(directory / "trace-environment.sh")}
        (directory / "evidence.json").write_text(json.dumps(record))
        return directory, record

    def test_trace_provenance_negative_cases(self):
        for change in [{"base": "other"}, {"head": "other"}, {"sources": {}}, {"exit": 1},
                       {"finished_ns": 0}, {"parser": "future"}, {"bash": "old"},
                       {"trace_format": 2}, {"trace_format": True}, {"bash": "GNU bash, version 5.3.150(1)"},
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
            original_checked = self.collector.checked
            def checked(command, **kwargs):
                return b"head\n" if command[:3] == ["git", "rev-parse", "HEAD"] else original_checked(command, **kwargs)
            with mock.patch.object(self.collector, "resolve_commit", return_value="base"), \
                    mock.patch.object(self.collector, "checked", side_effect=checked), \
                    mock.patch.object(self.collector, "changed_rows", return_value={"Scripts/probe.sh": {1}}), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                self.assertEqual(self.collector.main(arguments), 0)
                self.assertEqual(json.loads(output.getvalue())["production"]["covered"], 1)
            with mock.patch.object(self.collector, "resolve_commit", return_value="base"), \
                    mock.patch.object(self.collector, "checked", side_effect=checked), \
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
