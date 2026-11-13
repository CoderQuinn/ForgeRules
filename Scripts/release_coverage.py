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
PARSER_VERSION = "v3.12.0"
EXECUTABLE = {"CallExpr", "DeclClause", "TestClause", "ArithmCmd", "LetClause",
              "ForClause", "CaseClause", "TimeClause", "CoprocClause"}
CONTAINERS = {"BinaryCmd", "Block", "IfClause", "WhileClause", "Subshell", "FuncDecl"}


class CoverageError(Exception):
    pass


def checked(command, **kwargs):
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs)
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

    def visit(node, substitutions=()):
        if isinstance(node, list):
            for child in node:
                visit(child, substitutions)
        elif isinstance(node, dict):
            if node.get("Type") in {"CmdSubst", "ProcSubst"}:
                substitutions += (node["End"]["Line"],)
            if "Cmd" in node:
                command = node["Cmd"]
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
                                  "fingerprint": fingerprint,
                                  "runtime_lines": sorted({anchor, command["End"]["Line"], *substitutions})})
            for value in node.values():
                visit(value, substitutions)
    visit(tree)
    return sites


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(root):
    return {str(path.relative_to(root)): digest(path)
            for path in sorted((root / "Scripts").rglob("*"))
            if path.is_file() and path.suffix in {".sh", ".py"}}


def changed_rows(root, base):
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


def summarize(root, sources, hits, changed):
    if sources != snapshot(root):
        raise CoverageError("stale or incomplete source snapshot")
    files = []
    groups = {name: [set(), set()] for name in ("production", "changed_production", "support", "changed_support")}
    for name in sources:
        if not name.endswith(".sh"):
            continue
        if name not in hits:
            raise CoverageError("missing trace source: " + name)
        sites = command_sites((root / name).read_bytes())
        owned = {line for site in sites for line in site["rows"]}
        covered, ambiguous = set(), []
        for runtime, fingerprint in hits[name]:
            candidates = [site for site in sites if runtime in site["runtime_lines"] and fingerprint == site["fingerprint"]]
            if len({tuple(site["rows"]) for site in candidates}) > 1:
                ambiguous.append({"runtime_row": runtime, "fingerprint": fingerprint,
                                  "candidate_anchors": sorted(site["anchor"] for site in candidates)})
                continue
            for site in candidates:
                covered.update(site["rows"])
        support = "/fixtures/" in name or Path(name).name.startswith("test-")
        group = "support" if support else "production"
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
    for path in sorted((directory / "temporary-events").glob("*.events")):
        fields = path.read_bytes().split(b"\0")
        if fields[-1] != b"" or (len(fields) - 1) % 4:
            raise CoverageError("malformed native trace events")
        for index in range(0, len(fields) - 1, 4):
            source, row, command, source_sha = fields[index:index + 4]
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
            number = int(row)
            if number <= 0:
                raise CoverageError("invalid native trace row")
            events.add((name, number, event_fingerprint(command)))
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
                or type(record["trace_format"]) is not int or record["trace_format"] != 1):
            raise CoverageError("unsupported trace tool versions")
        if digest(directory / "trace-environment.sh") != record["helper_sha256"]:
            raise CoverageError("stale trace helper")
        events = directory / "events.json"
        if digest(events) != record["events_sha256"]:
            raise CoverageError("stale trace digest")
        for name, row, fingerprint in json.loads(events.read_text())["events"]:
            if (name not in sources or not isinstance(row, int) or isinstance(row, bool) or row <= 0
                    or not isinstance(fingerprint, str)
                    or not re.fullmatch(r"[0-9a-f]{64}|control:(ForClause|CaseClause|TimeClause|CoprocClause)", fingerprint)):
                raise CoverageError("malformed trace event")
            hits.setdefault(name, set()).add((row, fingerprint))
        records.append(record)
    if not records:
        raise CoverageError("missing trace evidence")
    # Manifest-owned but unexecuted sources are explicit zeroes, not absent.
    for name in sources:
        hits.setdefault(name, set())
    return sources, hits, records


def collect(root, directory, entry, base):
    root = root.resolve()
    _, parser_version = tool("shfmt", PARSER_VERSION)
    bash, bash_version = tool("bash", "5.3.15")
    sources = snapshot(root)
    directory.mkdir(parents=True, exist_ok=False)
    (directory / "temporary-events").mkdir()
    helper = directory / "trace-environment.sh"
    helper.write_text('''# Native Bash DEBUG tracing; unexpanded command syntax is temporary only.
__forge_main_sha=""
if [[ -f "$0" ]]; then
    __forge_main_sha="$(shasum -a 256 "$0")"
    __forge_main_sha="${__forge_main_sha%% *}"
fi
set -T
trap 'printf "%s\\0%s\\0%s\\0%s\\0" "${BASH_SOURCE[0]:-$0}" "$LINENO" "$BASH_COMMAND" "$__forge_main_sha" >>"${FORGERULES_COVERAGE_EVENTS}/${BASHPID}.events"' DEBUG
''')
    started = time.time_ns()
    environment = os.environ.copy()
    environment.update({"PATH": str(Path(bash).parent) + os.pathsep + environment["PATH"],
                        "BASH_ENV": str(helper), "FORGERULES_COVERAGE_EVENTS": str(directory / "temporary-events")})
    command = [bash, str(root / entry)]
    with (directory / "run.log").open("wb") as log:
        result = subprocess.run(command, cwd=root, env=environment, stdout=log, stderr=subprocess.STDOUT)
    if sources != snapshot(root):
        raise CoverageError("source changed during trace collection")
    events = finalize_events(root, directory, sources)
    record = {"sources": sources, "base": base,
              "head": checked(["git", "rev-parse", "HEAD"], cwd=root).decode().strip(),
              "parser": parser_version, "bash": bash_version, "trace_format": 1,
              "command": command, "exit": result.returncode, "started_ns": started,
              "finished_ns": time.time_ns(), "events_sha256": digest(events), "helper_sha256": digest(helper)}
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
    args = parser.parse_args(argv)
    root = args.root.resolve()
    try:
        if args.action == "collect":
            if len(args.directories) != 1:
                raise CoverageError("collect requires one fresh output directory")
            collect(root, args.directories[0].resolve(), args.entry, args.base)
            return 0
        head = checked(["git", "rev-parse", "HEAD"], cwd=root).decode().strip()
        sources, hits, records = load_evidence(root, args.directories, args.base, head)
        result = summarize(root, sources, hits, changed_rows(root, args.base))
        result.update({"base": args.base, "head": head, "sources": sources, "runs": records})
        print(json.dumps(result, indent=2))
        return 0 if result["production"]["passed"] and result["changed_production"]["passed"] else 1
    except (CoverageError, OSError, ValueError, KeyError, TypeError) as error:
        print("coverage error: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
