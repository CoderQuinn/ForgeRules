#!/usr/bin/env python3
"""Pinned syntax-derived Bash command-source-row coverage, backed by native Bash DEBUG events."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
from functools import lru_cache


BASE = "1dc34fed52480cad816843e66495461a3058dbef"
MINIMUM = 95.0
HOOK = Path(__file__).resolve().parent / "coverage/release-trace-hook.sh"
PARSER_VERSION = "v3.12.0"
EXECUTABLE = {"CallExpr", "DeclClause", "TestClause", "ArithmCmd", "LetClause",
              "ForClause", "CaseClause", "TimeClause", "CoprocClause"}
CONTAINERS = {"BinaryCmd", "Block", "IfClause", "WhileClause", "Subshell", "FuncDecl"}


class CoverageError(Exception):
    pass


def checked(command, **kwargs):
    result = subprocess.run(command, shell=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs)
    if result.returncode:
        raise CoverageError("command failed: " + repr(command) + "\n" + result.stderr.decode())
    return result.stdout


def tool(name, version):
    path = os.environ.get(name.upper(), name)
    value = checked([path, "--version"]).decode().strip()
    valid = value.startswith("GNU bash, version " + version + "(") if name == "bash" else value == version
    if not valid:
        raise CoverageError("unsupported " + name + " version: " + value)
    return shutil.which(path) or path, value


@lru_cache(maxsize=None)
def syntax_fingerprint(source):
    parser, _ = tool("shfmt", PARSER_VERSION)
    normalized = checked([parser, "-ln", "bash", "-mn"], input=source)
    tree = json.loads(checked([parser, "-ln", "bash", "--to-json"], input=normalized))

    def structural(node):
        if isinstance(node, list):
            return [structural(child) for child in node]
        if isinstance(node, dict):
            if node.get("Op") == 59 and node.get("N", {}).get("Value") == "1":
                node = {key: value for key, value in node.items() if key != "N"}
            if node.get("Type") == "FuncDecl":
                node = {key: value for key, value in node.items() if key not in {"Parens", "RsrvWord"}}
            if node.get("Type") == "SglQuoted" and node.get("Dollar"):
                # Bash deparses the authored $'\t' into a literal quoted tab.
                # Normalize the two spellings without evaluating source code.
                value = node["Value"]
                escapes = {"\\t": "\t", "\\n": "\n", "\\r": "\r", "\\\\": "\\"}
                def decode(match):
                    if match[0] not in escapes:
                        raise CoverageError("unsupported ANSI quote escape: " + match[0])
                    return escapes[match[0]]
                node = {key: value for key, value in node.items() if key != "Dollar"}
                node["Value"] = re.sub(r"\\.", decode, value)
            return {key: structural(value) for key, value in node.items()
                    if key != "Comments" and not (isinstance(value, dict) and "Offset" in value)}
        return node
    canonical = json.dumps(structural(tree), sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(canonical).hexdigest()


@lru_cache(maxsize=None)
def function_definitions(source):
    parser, _ = tool("shfmt", PARSER_VERSION)
    tree = json.loads(checked([parser, "-ln", "bash", "--to-json"], input=source))
    definitions = {}
    def visit(node):
        if isinstance(node, list):
            for child in node:
                visit(child)
        elif isinstance(node, dict):
            if node.get("Type") == "FuncDecl":
                syntax = source[node["Pos"]["Offset"]:node["End"]["Offset"]]
                definitions.setdefault(node["Name"]["Value"], []).append(syntax_fingerprint(syntax))
            for child in node.values():
                visit(child)
    visit(tree)
    return definitions


def resolve_commit(root, value):
    if not value or value.startswith("-"):
        raise CoverageError("base must name a commit, not an option")
    result = checked(["git", "rev-parse", "--verify", "--end-of-options", value + "^{commit}"], cwd=root).decode().strip()
    if not re.fullmatch(r"[0-9a-f]{40}", result):
        raise CoverageError("base did not resolve to one commit")
    return result


def command_sites(source):
    """Own token starts, not literal interiors; identify real command syntax."""
    parser, _ = tool("shfmt", PARSER_VERSION)
    tree = json.loads(checked([parser, "-ln", "bash", "--to-json"], input=source))
    sites = []

    def word_rows(node):
        if isinstance(node, list):
            return set().union(*(word_rows(child) for child in node))
        if not isinstance(node, dict):
            return set()
        rows = {node["Pos"]["Line"]} if "Pos" in node else set()
        if node.get("Type") in {"Word", "SglQuoted", "DblQuoted", "Lit", "CmdSubst", "ParamExp"}:
            return rows
        for key in ("X", "Y", "Args", "Elems", "Value", "Array", "Init", "Cond", "Post"):
            rows.update(word_rows(node.get(key)))
        if "OpPos" in node:
            rows.add(node["OpPos"]["Line"])
        return rows

    def visit(node, substitutions=(), function=None, entered_redirect_rows=()):
        if isinstance(node, list):
            for child in node:
                visit(child, substitutions, function, entered_redirect_rows)
        elif isinstance(node, dict):
            compound_redirect_rows = set()
            if node.get("Type") == "FuncDecl":
                syntax = source[node["Pos"]["Offset"]:node["End"]["Offset"]]
                function = {"name": node["Name"]["Value"], "fingerprint": syntax_fingerprint(syntax)}
                # A body may run long after its declaration's enclosing block.
                entered_redirect_rows = ()
            if node.get("Type") in {"CmdSubst", "ProcSubst"}:
                substitutions += (node["End"]["Line"],)
            if "Cmd" not in node and node.get("Redirs"):
                rows = set()
                for redirect in node["Redirs"]:
                    rows.update(word_rows(redirect))
                    rows.update(word_rows(redirect.get("Word")))
                syntax = source[node["Pos"]["Offset"]:node["End"]["Offset"]]
                sites.append({"kind": "RedirectOnly", "anchor": node["Pos"]["Line"], "rows": sorted(rows),
                              "fingerprint": syntax_fingerprint(syntax), "function": function,
                              "entered_redirect_rows": sorted(entered_redirect_rows),
                              "runtime_lines": sorted({node["Pos"]["Line"], node["End"]["Line"], *substitutions})})
            if "Cmd" in node:
                command = node["Cmd"]
                if command["Type"] in CONTAINERS and node.get("Redirs"):
                    if command["Type"] not in {"Block", "WhileClause"}:
                        raise CoverageError("unsupported compound command redirection")
                    for redirect in node["Redirs"]:
                        compound_redirect_rows.update(word_rows(redirect))
                        compound_redirect_rows.update(word_rows(redirect.get("Word")))
                    sites.append({"kind": command["Type"] + "Redirection",
                                  "anchor": node["Redirs"][0]["Pos"]["Line"],
                                  "rows": sorted(compound_redirect_rows), "fingerprint": None,
                                  "function": function, "runtime_lines": []})
                kind = command["Type"]
                if kind not in EXECUTABLE | CONTAINERS:
                    raise CoverageError("unsupported Bash command node: " + kind)
                if kind in EXECUTABLE:
                    anchor = command["Pos"]["Line"]
                    rows = {anchor}
                    for key in ("Args", "Assigns", "X", "Word"):
                        rows.update(word_rows(command.get(key)))
                    loop = command.get("Loop", {})
                    for key in ("Items", "Init", "Cond", "Post"):
                        rows.update(word_rows(loop.get(key)))
                    syntax = source[command["Pos"]["Offset"]:command["End"]["Offset"]]
                    for redirect in node.get("Redirs", []):
                        rows.update(word_rows(redirect))
                        rows.update(word_rows(redirect.get("Word")))
                        syntax += b" " + source[redirect["Pos"]["Offset"]:redirect["End"]["Offset"]]
                    fingerprint = "control:" + kind if kind in {"ForClause", "CaseClause", "TimeClause", "CoprocClause"} else syntax_fingerprint(syntax)
                    sites.append({"kind": kind, "anchor": anchor, "rows": sorted(rows),
                                  "fingerprint": fingerprint, "function": function,
                                  "entered_redirect_rows": sorted(entered_redirect_rows),
                                  "runtime_lines": sorted({anchor, command["End"]["Line"], *substitutions})})
            for key, value in node.items():
                # Bash applies container redirects before its commands/conditions,
                # but target substitutions run before redirect success is known.
                entered = set(entered_redirect_rows)
                if key == "Cmd":
                    entered.update(compound_redirect_rows)
                visit(value, substitutions, function, tuple(sorted(entered)))
    visit(tree)
    return sites


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(root):
    return {str(path.relative_to(root)): digest(path)
            for path in sorted((root / "Scripts").rglob("*"))
            if path.is_file() and path.suffix in {".sh", ".py"}}


def changed_rows(root, base):
    base = resolve_commit(root, base)
    diff = checked(["git", "diff", "--no-ext-diff", "--unified=0", base, "--", "Scripts"], cwd=root).decode()
    changed = {}
    current = None
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            current = line[6:]
            changed[current] = set()
        match = re.match(r"@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", line)
        if match and current:
            first, count = int(match[1]), int(match[2] or "1")
            changed[current].update(range(first, first + count))
    return changed


def fraction(covered, owned):
    percent = 100.0 * len(covered) / len(owned) if owned else None
    return {"covered": len(covered), "total": len(owned), "percent": percent,
            "passed": percent is not None and percent >= MINIMUM}


def summarize(root, sources, hits, changed, hook=None):
    if sources != snapshot(root):
        raise CoverageError("stale or incomplete source snapshot")
    files = []
    groups = {name: [set(), set()] for name in ("production", "changed_production", "support", "changed_support", "tooling", "changed_tooling")}
    for name in sources:
        if not name.endswith(".sh"):
            continue
        if name not in hits:
            raise CoverageError("missing trace source: " + name)
        sites = command_sites((root / name).read_bytes())
        owned = {line for site in sites for line in site["rows"]}
        covered, ambiguous = set(), []
        for event in hits[name]:
            runtime, fingerprint, *context = event
            function_name, definition = context if context else ("", "")
            function = {"name": function_name, "fingerprint": definition} if function_name else None
            candidates = [site for site in sites if runtime in site["runtime_lines"]
                          and fingerprint == site["fingerprint"] and function == site["function"]]
            if len({(tuple(site["rows"]), tuple(site.get("entered_redirect_rows", [])))
                    for site in candidates}) > 1:
                ambiguous.append({"runtime_row": runtime, "fingerprint": fingerprint,
                                  "candidate_anchors": sorted(site["anchor"] for site in candidates)})
                continue
            for site in candidates:
                covered.update(site["rows"])
                covered.update(site.get("entered_redirect_rows", []))
        support = "/fixtures/" in name or Path(name).name.startswith("test-")
        group = "tooling" if name == "Scripts/coverage/release-trace-hook.sh" else ("support" if support else "production")
        if group == "tooling" and hook is not None:
            if hook["source_sha256"] != sources[name] or not set(hook["covered_rows"]) <= owned:
                raise CoverageError("stale or malformed hook coverage")
            covered = set(hook["covered_rows"])
        added = owned & changed.get(name, set())
        files.append({"path": name, "scope": group, **fraction(covered, owned),
                      "owned": sorted(owned), "covered_rows": sorted(covered),
                      "uncovered": sorted(owned - covered),
                      "changed": fraction(covered & added, added),
                      "changed_uncovered": sorted(added - covered), "sites": sites,
                      "ambiguous_events": sorted(ambiguous, key=lambda event: (event["runtime_row"], event["fingerprint"]))})
        for key, denominator in ((group, owned), ("changed_" + group, added)):
            groups[key][0].update((name, line) for line in covered & denominator)
            groups[key][1].update((name, line) for line in denominator)
    if not files:
        raise CoverageError("missing Bash production source")
    return {"files": files, **{key: fraction(*values) for key, values in groups.items()}}


def file_identity(path):
    def identity(value):
        return [value.st_dev, value.st_ino, value.st_mode, value.st_size, value.st_mtime_ns, value.st_ctime_ns]
    return {"stat": identity(path.stat()), "lstat": identity(path.lstat()),
            "link": os.readlink(path) if path.is_symlink() else None}


def hook_coverage(data, hook=HOOK):
    """Measure the hook using independent xtrace, never its DEBUG event output."""
    runs = data.get("runs", [])
    if not runs:
        raise CoverageError("missing independent hook trace")
    if data.get("tests_passed") is not True:
        raise CoverageError("failed or incomplete hook fixtures")
    current_sha = digest(hook)
    sites = command_sites(hook.read_bytes())
    owned = {row for site in sites for row in site["rows"]}
    observed, by_version = set(), {}
    for run in runs:
        source = Path(run["source_path"])
        if (run["source_sha256"] != current_sha or digest(source) != current_sha
                or run["before"] != run["after"] or file_identity(source) != run["after"]):
            raise CoverageError("stale independent hook source")
        if run["parser"] != PARSER_VERSION or run["exit"] != 0 or run["parity"] is not True:
            raise CoverageError("failed hook trace/parity evidence")
        version = run["bash"]
        if not version.startswith(("5.3.15(", "3.2.57(")):
            raise CoverageError("unsupported hook trace Bash")
        nonce = run["nonce"]
        if not re.fullmatch(r"[0-9a-f]{16}|[0-9a-f]{32}", nonce):
            raise CoverageError("malformed hook trace nonce")
        trace_path = Path(run["trace_path"])
        raw = trace_path.read_bytes()
        trailer = ("FORGE_META_COMPLETE_" + nonce + "\n").encode()
        if digest(trace_path) != run["trace_sha256"] or not raw.endswith(trailer):
            raise CoverageError("stale or truncated independent hook trace")
        prefix = re.compile(rb"^\++FORGE_META_" + nonce.encode() + rb"\t([^\t]+)\t([0-9]+)\t([^\t]+)\t([0-9]+)\t")
        rows = set()
        for line in raw.splitlines():
            event = prefix.match(line)
            event_source = Path(os.fsdecode(event[3])) if event else None
            if event_source is not None and not event_source.is_absolute():
                event_source = Path(run.get("cwd", ".")) / event_source
            if event and event_source.resolve() == source.resolve():
                if event[1].decode() != version or int(event[2]) <= 0 or int(event[4]) <= 0:
                    raise CoverageError("malformed independent hook event")
                rows.add(int(event[4]))
        if not rows:
            raise CoverageError("missing hook source events")
        observed.update(rows)
        by_version.setdefault(version, set()).update(rows)
    covered = {row for site in sites if set(site["runtime_lines"]) & observed for row in site["rows"]}
    versions = {version: sorted({row for site in sites if set(site["runtime_lines"]) & rows for row in site["rows"]})
                for version, rows in by_version.items()}
    return {**fraction(covered, owned), "source_sha256": current_sha, "owned": sorted(owned),
            "covered_rows": sorted(covered), "uncovered": sorted(owned - covered),
            "rows_by_bash_version": versions, "runs": runs}


def event_fingerprint(command):
    for prefix, kind in ((b"for ", "ForClause"), (b"case ", "CaseClause"),
                         (b"time ", "TimeClause"), (b"coproc ", "CoprocClause")):
        if command.startswith(prefix):
            return "control:" + kind
    return syntax_fingerprint(command)


def finalize_events(root, directory, sources):
    root = root.resolve()
    aliases = {}
    for name, sha in sources.items():
        aliases.setdefault(sha, []).append(name)
    events, external = set(), set()
    failure_flag = directory / "write-failures"
    if failure_flag.exists() and failure_flag.stat().st_size:
        raise CoverageError("incomplete trace: hook write failure")
    if list((directory / "temporary-events").glob("*.unsupported")):
        raise CoverageError("unsupported child Bash interpreter")
    for path in sorted((directory / "temporary-events").glob("*.events")):
        fields = path.read_bytes().split(b"\0")
        if fields[-1] != b"" or (len(fields) - 1) % 7:
            raise CoverageError("malformed native trace events")
        for index in range(0, len(fields) - 1, 7):
            source, row, command, source_sha, bash_version, function_name, definition_id = fields[index:index + 7]
            if not bash_version.decode().startswith("5.3.15("):
                raise CoverageError("unsupported event Bash interpreter")
            name = Path(source.decode())
            name = (root / name).resolve() if not name.is_absolute() else name.resolve()
            if root in name.parents:
                name = str(name.relative_to(root))
            else:
                matched = aliases.get(source_sha.decode(), [])
                if len(matched) != 1:
                    external.add(str(name))
                    continue
                name = matched[0]
            if name not in sources:
                external.add(str(name))
                continue
            if source_sha.decode() != sources[name]:
                raise CoverageError("stale runtime source digest: " + name)
            function_name = function_name.decode()
            definition = ""
            if function_name:
                identity = definition_id.decode()
                if not re.fullmatch(r"[0-9a-f]{64}", identity):
                    raise CoverageError("missing runtime function definition")
                declared = directory / "temporary-events" / (identity + ".definition")
                if digest(declared) != identity:
                    raise CoverageError("stale runtime function definition")
                definition = syntax_fingerprint(declared.read_bytes())
                approved = function_definitions((root / name).read_bytes()).get(function_name, [])
                if approved != [definition]:
                    raise CoverageError("unapproved or ambiguous runtime function definition: " + name + "/" + function_name)
            number = int(row)
            if number <= 0:
                raise CoverageError("invalid native trace row")
            events.add((name, number, event_fingerprint(command), source_sha.decode(), bash_version.decode(), function_name, definition))
    if not events:
        raise CoverageError("missing native trace events")
    output = directory / "events.json"
    output.write_text(json.dumps({"events": sorted(events), "external_sources": sorted(external)}, indent=2) + "\n")
    shutil.rmtree(directory / "temporary-events")
    return output


def load_evidence(root, directories, base, head):
    sources, hits, records = snapshot(root), {}, []
    for directory in directories:
        record = json.loads((directory / "evidence.json").read_text())
        if record["sources"] != sources or record["base"] != base or record["head"] != head:
            raise CoverageError("stale source/revision evidence")
        if record["exit"] != 0 or record["finished_ns"] < record["started_ns"]:
            raise CoverageError("failed or incomplete trace run")
        if (record["parser"] != PARSER_VERSION or not record["bash"].startswith("GNU bash, version 5.3.15(")
                or type(record["trace_format"]) is not int or record["trace_format"] != 3):
            raise CoverageError("unsupported trace tool versions")
        if digest(directory / "trace-environment.sh") != record["helper_sha256"]:
            raise CoverageError("stale trace helper")
        events = directory / "events.json"
        if digest(events) != record["events_sha256"]:
            raise CoverageError("stale trace digest")
        for name, row, fingerprint, runtime_sha, runtime_bash, function_name, definition in json.loads(events.read_text())["events"]:
            if name not in sources or runtime_sha != sources[name] or not runtime_bash.startswith("5.3.15("):
                raise CoverageError("stale source digest or unsupported event Bash")
            if (name not in sources or not isinstance(row, int) or isinstance(row, bool) or row <= 0
                    or not isinstance(fingerprint, str)
                    or not re.fullmatch(r"[0-9a-f]{64}|control:(ForClause|CaseClause|TimeClause|CoprocClause)", fingerprint)):
                raise CoverageError("malformed trace event")
            if function_name and function_definitions((root / name).read_bytes()).get(function_name, []) != [definition]:
                raise CoverageError("unapproved runtime function definition")
            if not function_name and definition:
                raise CoverageError("malformed runtime function context")
            hits.setdefault(name, set()).add((row, fingerprint, function_name, definition))
        records.append(record)
    if not records:
        raise CoverageError("missing trace evidence")
    # Manifest-owned but unexecuted sources are explicit zeroes, not absent.
    for name in sources:
        hits.setdefault(name, set())
    return sources, hits, records


def collect(root, directory, entry, base):
    root = root.resolve()
    base = resolve_commit(root, base)
    _, parser_version = tool("shfmt", PARSER_VERSION)
    bash, bash_version = tool("bash", "5.3.15")
    sources = snapshot(root)
    directory.mkdir(parents=True, exist_ok=False)
    (directory / "temporary-events").mkdir()
    (directory / "write-failures").write_text("")
    helper = directory / "trace-environment.sh"
    shutil.copyfile(HOOK, helper)
    started = time.time_ns()
    environment = os.environ.copy()
    environment.update({"PATH": str(Path(bash).parent) + os.pathsep + environment["PATH"],
                        "BASH_ENV": str(helper), "FORGERULES_COVERAGE_EVENTS": str(directory / "temporary-events"),
                        "FORGERULES_COVERAGE_HASHER": "/usr/bin/openssl", "FORGERULES_COVERAGE_FAILURE": str(directory / "write-failures")})
    command = [bash, str(root / entry)]
    with (directory / "run.log").open("wb") as log:
        result = subprocess.run(command, shell=False, cwd=root, env=environment, stdout=log, stderr=subprocess.STDOUT)
    if sources != snapshot(root):
        raise CoverageError("source changed during trace collection")
    events = finalize_events(root, directory, sources)
    record = {"sources": sources, "base": base,
              "head": checked(["git", "rev-parse", "HEAD"], cwd=root).decode().strip(),
              "parser": parser_version, "bash": bash_version, "trace_format": 3,
              "command": command, "exit": result.returncode, "started_ns": started,
              "finished_ns": time.time_ns(), "events_sha256": digest(events), "helper_sha256": digest(helper),
              "hash_backend": checked(["/usr/bin/openssl", "version"]).decode().strip()}
    (directory / "evidence.json").write_text(json.dumps(record, indent=2) + "\n")
    if result.returncode:
        raise CoverageError("trace command failed; inspect " + str(directory / "run.log"))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("collect", "report"))
    parser.add_argument("directories", nargs="+", type=Path)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--base", default=BASE)
    parser.add_argument("--entry", default="Scripts/test-verify-release-assets.sh")
    parser.add_argument("--hook-evidence", type=Path)
    args = parser.parse_args(argv)
    root = args.root.resolve()
    try:
        if args.action == "collect":
            if len(args.directories) != 1:
                raise CoverageError("collect requires one fresh output directory")
            collect(root, args.directories[0].resolve(), args.entry, args.base)
            return 0
        head = checked(["git", "rev-parse", "HEAD"], cwd=root).decode().strip()
        base = resolve_commit(root, args.base)
        sources, hits, records = load_evidence(root, args.directories, base, head)
        hook = hook_coverage(json.loads(args.hook_evidence.read_text())) if args.hook_evidence else None
        result = summarize(root, sources, hits, changed_rows(root, base), hook)
        result.update({"base": base, "head": head, "sources": sources, "runs": records})
        result["hook_tooling"] = hook if hook is not None else {"passed": False, "percent": None, "reason": "missing independent hook evidence"}
        print(json.dumps(result, indent=2))
        return 0 if result["production"]["passed"] and result["changed_production"]["passed"] and hook and hook["passed"] else 1
    except (CoverageError, OSError, ValueError, KeyError, TypeError) as error:
        print("coverage error: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
