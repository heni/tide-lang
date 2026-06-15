#!/usr/bin/env python3
"""Measure how far each example gets through the tide build pipeline.

Source of truth is ``examples/auto-status.json`` (deterministic — no
timestamps or commit SHAs, so it diffs cleanly). ``examples/STATUS.md``
is a human view generated from it. The metric we grow is ``build_ok``:
the number of examples that compile end-to-end.

    scripts/corpus_status.py            regenerate the JSON + STATUS.md
    scripts/corpus_status.py --check    fail if the snapshot is stale
                                        or if build_ok regressed (ratchet)
    scripts/corpus_status.py --history  derive the full trend from git
                                        (git log of auto-status.json) as JSONL

History is never maintained — git is the append-only log; --history
reconstructs it on demand, so there is no parallel file to drift.

Written in Python as interim dev tooling. The intent is to rewrite it
in Tide once feature-coverage is sufficient (subprocess, JSON, file
walking, string ops — all map onto Tide + Go-stdlib bindings), as a
self-hosting/dogfooding milestone. Kept deliberately straightforward
and idiom-light to make that port mechanical.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile

SCHEMA_VERSION = 2
JSON_PATH = "examples/auto-status.json"
MD_PATH = "examples/STATUS.md"
FLOORS_PATH = "examples/metric-floors.toml"

# Diagnostics emitted before a file parses; any other error code means
# the file parsed and a later stage rejected it.
PARSE_CODES = {"E0101", "E0102", "E0107", "E0109", "E0110", "E0111", "E0112"}
EMIT_CODES = {"E0801", "E0802", "E0803"}
STAGE_ORDER = {"build": 0, "emit": 1, "sema": 2, "parse": 3}


def stage_of(code: str) -> str:
    if code in PARSE_CODES:
        return "parse"
    if code in EMIT_CODES:
        return "emit"
    return "sema"


def read_floors() -> dict:
    """Parse the committed metric floors. A malformed/missing file is a
    tooling failure (the metric can't be enforced) → caller exits red."""
    floors = {}
    try:
        text = open(FLOORS_PATH).read()
    except FileNotFoundError:
        sys.exit(f"corpus-status: {FLOORS_PATH} missing — the metric floors are unset")
    for key in ("build_ok_min", "diag_ok_min"):
        m = re.search(rf"^\s*{key}\s*=\s*(\d+)\s*(?:#.*)?$", text, re.MULTILINE)
        if not m:
            sys.exit(f"corpus-status: {FLOORS_PATH} missing or malformed key {key!r}")
        floors[key] = int(m.group(1))
    return floors


def repo_root() -> str:
    return subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()


def corpus_files() -> list[str]:
    # Only git-tracked files, so the snapshot reflects the committed
    # suite and reproduces on a clean checkout (untracked scratch
    # .td files don't perturb the metric).
    out = subprocess.check_output(
        ["git", "ls-files", "examples", "user_tests"], text=True
    )
    return sorted(p for p in out.splitlines() if p.endswith(".td"))


def build_tide(tmp: str) -> str:
    binpath = os.path.join(tmp, "tide")
    r = subprocess.run(
        ["go", "build", "-o", binpath, "./cmd/tide"],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        sys.exit(f"corpus-status: building tide failed:\n{r.stderr}")
    return binpath


def _diag_pos(line: str) -> int:
    """Index of a tide diagnostic marker (`error[` / `internal[`) in line, or -1."""
    for marker in ("error[", "internal["):
        i = line.find(marker)
        if i >= 0:
            return i
    return -1


def error_code(line: str) -> str:
    i = _diag_pos(line)
    if i < 0:
        return ""
    rest = line[line.find("[", i) + 1:]
    j = rest.find("]")
    return rest[:j] if j >= 0 else ""


def first_diag_line(out: str) -> str:
    for ln in out.splitlines():
        k = _diag_pos(ln)
        if k >= 0:
            return ln[k:].strip()  # strip "<path>:<line>:<col>: " position
    return ""


def classify(tide: str, file: str, out: str) -> tuple[str, str]:
    r = subprocess.run(
        [tide, "build", "-o", out, file],
        capture_output=True, text=True,
    )
    if r.returncode == 0:
        return "build", ""
    combined = (r.stdout or "") + (r.stderr or "")
    diag = first_diag_line(combined)
    code = error_code(diag)
    if code in PARSE_CODES:
        return "parse", diag
    if code in EMIT_CODES:
        return "emit", diag
    if "go build failed" in combined:
        # Raw Go-toolchain output is non-deterministic; record a stable
        # blocker. (A tide-side //line-mapped diagnostic, if any, is
        # preferred above via EMIT_CODES.)
        return "emit", "go build failed"
    if code:
        return "sema", diag
    return "emit", diag or "unknown failure"


# --- Negative cases: the "correct error messages" metric (diag_ok) ---------
#
# Each [[error]] in an example.toml is a patch that injects a mistake plus the
# diagnostic it must provoke. diag_ok counts the cases that reproduce their
# expected diagnostic (code set ⊆ emitted, stage matches, message substrings
# present). A case that fails to reproduce is a *penalty* on diag_ok — never a
# tooling error. A patch that no longer applies, or a compiler/subprocess that
# cannot be run, *is* a tooling error and aborts the whole run (CI red): the
# measurement itself is broken, so the metric would be a lie.


def _toml_scalar(line: str) -> str:
    _, _, v = line.partition("=")
    v = v.strip()
    if v.startswith('"'):
        j = v.find('"', 1)
        if j >= 0:
            return v[1:j]
    return v.split("#")[0].strip().strip('"')


def _toml_codes(line: str) -> list[str]:
    _, _, v = line.partition("=")
    v = v.split("]")[0]
    return re.findall(r'"([^"]*)"', v)


def parse_neg_cases(manifest: str) -> list[dict]:
    """Extract [[error]] entries from one example.toml (TOML-lite, mirrors
    cmd/tide/negative_test.go so the metric needs no third-party parser)."""
    cases, cur, entry = [], None, ""
    for raw in open(manifest):
        line = raw.strip()
        if line == "[[error]]":
            if cur:
                cases.append(cur)
            cur = {"patch": "", "expect": [], "stage": "", "matches": ""}
        elif line.startswith("entry"):
            entry = _toml_scalar(line)
        elif line.startswith("["):
            if cur:
                cases.append(cur)
            cur = None
        elif cur is not None:
            if line.startswith("patch"):
                cur["patch"] = _toml_scalar(line)
            elif line.startswith("expect"):
                cur["expect"] = _toml_codes(line)
            elif line.startswith("stage"):
                cur["stage"] = _toml_scalar(line)
            elif line.startswith("matches"):
                cur["matches"] = _toml_scalar(line)
    if cur:
        cases.append(cur)
    d = os.path.dirname(manifest)
    for c in cases:
        c["dir"], c["entry"] = d, entry
    return cases


def eval_negatives(tide: str) -> tuple[int, int, list]:
    """Return (diag_ok, diag_total, misses). The .expected sidecar is the
    *ideal* diagnostic the user deserves; diag_ok counts cases whose actual
    output already meets it. A case that falls short is a miss (the
    diagnostic-quality backlog), NOT a tooling error. A patch that won't apply
    or a subprocess that can't run IS a tooling error and aborts (CI red)."""
    code_re = re.compile(r"error\[(E\d+)\]")
    tracked = subprocess.check_output(["git", "ls-files", "examples"], text=True)
    manifests = sorted(
        p for p in tracked.splitlines() if os.path.basename(p) == "example.toml"
    )
    diag_ok = diag_total = 0
    misses = []
    with tempfile.TemporaryDirectory(prefix="corpus-neg-") as work:
        for mf in manifests:
            for c in parse_neg_cases(mf):
                if not c["patch"] or not c["expect"] or not c["entry"]:
                    continue
                diag_total += 1
                case = f"{c['dir']}/{c['patch']}"
                tmp = tempfile.mkdtemp(dir=work)
                base = os.path.join(c["dir"], c["entry"])
                staged = os.path.join(tmp, c["entry"])
                with open(base) as src, open(staged, "w") as dst:
                    dst.write(src.read())
                # Absolute: `patch -d tmp` resolves a relative -i against tmp.
                patchfile = os.path.abspath(os.path.join(c["dir"], c["patch"]))
                ap = subprocess.run(
                    ["patch", "-p0", "-s", "-d", tmp, "-i", patchfile],
                    capture_output=True, text=True,
                )
                if ap.returncode != 0:
                    sys.exit(f"corpus-status: patch no longer applies "
                             f"({case}) — regenerate this case:\n{ap.stdout}{ap.stderr}")
                r = subprocess.run(
                    [tide, "build", "-o", os.path.join(tmp, "out.bin"), staged],
                    capture_output=True, text=True,
                )
                combined = (r.stdout or "") + (r.stderr or "")
                want = ""
                if c["matches"]:
                    try:
                        want = open(os.path.join(c["dir"], c["matches"])).read().strip()
                    except FileNotFoundError:
                        want = ""
                got = first_diag_line(combined) or ("(built clean — no diagnostic)" if r.returncode == 0 else "(no diagnostic)")
                if r.returncode == 0:
                    misses.append({"case": case, "want": want, "got": got})
                    continue
                emitted = set(code_re.findall(combined))
                ok = bool(emitted) and all(
                    code in emitted and (not c["stage"] or stage_of(code) == c["stage"])
                    for code in c["expect"]
                )
                if ok and want:
                    for ln in want.splitlines():
                        ln = ln.strip()
                        if ln and ln not in combined:
                            ok = False
                            break
                if ok:
                    diag_ok += 1
                else:
                    misses.append({"case": case, "want": want, "got": got})
    misses.sort(key=lambda m: m["case"])
    return diag_ok, diag_total, misses


def collect() -> dict:
    with tempfile.TemporaryDirectory(prefix="corpus-status-") as tmp:
        tide = build_tide(tmp)
        outp = os.path.join(tmp, "out")
        files = corpus_files()
        rows, tot = [], {"parse": 0, "sema": 0, "emit": 0, "build": 0}
        for f in files:
            stage, blocker = classify(tide, f, outp)
            rows.append({"path": f, "stage": stage, "blocker": blocker})
            tot[stage] += 1
        diag_ok, diag_total, diag_misses = eval_negatives(tide)
    return {
        "schema_version": SCHEMA_VERSION,
        "totals": {
            "total": len(files),
            "parse_fail": tot["parse"],
            "sema_fail": tot["sema"],
            "emit_fail": tot["emit"],
            "build_ok": tot["build"],
            "diag_ok": diag_ok,
            "diag_total": diag_total,
        },
        "files": rows,
        "diag_misses": diag_misses,
    }


def dumps(status: dict) -> str:
    return json.dumps(status, indent=2, ensure_ascii=False) + "\n"


def render_markdown(s: dict) -> str:
    t = s["totals"]
    floors = read_floors()
    out = [
        "# Example conformance status\n",
        "Generated by `scripts/corpus_status.py` from "
        "`examples/auto-status.json` — do not edit by hand. "
        "Run `scripts/corpus_status.py` to refresh.\n",
        "Two tracked metrics, each with a CI-enforced floor in "
        "`metric-floors.toml`:\n",
        f"- **build_ok — {t['build_ok']} / {t['total']} examples build "
        f"end-to-end** (floor {floors['build_ok_min']}).",
        f"- **diag_ok — {t['diag_ok']} / {t['diag_total']} negative cases "
        f"produce their expected diagnostic** (floor {floors['diag_ok_min']}).\n",
        "| Stage reached | Count |",
        "|---|---|",
        f"| ✅ build (full pipeline) | {t['build_ok']} |",
        f"| emit / codegen fail | {t['emit_fail']} |",
        f"| sema fail | {t['sema_fail']} |",
        f"| parse fail | {t['parse_fail']} |",
        "",
        "## Per-example\n",
        "| Example | Stage | First blocker |",
        "|---|---|---|",
    ]
    rows = sorted(s["files"], key=lambda f: (STAGE_ORDER[f["stage"]], f["path"]))
    for f in rows:
        out.append(f"| `{f['path']}` | {f['stage']} | {f['blocker'] or '—'} |")

    misses = s.get("diag_misses", [])
    out += [
        "",
        "## Diagnostic-quality gaps\n",
        "Negative cases whose `.expected` records the **ideal** user-facing "
        "diagnostic that the compiler does not yet emit (e.g. a parser message "
        "still leaking internal token-kind names). This is the backlog the "
        "`diag_ok` metric grows toward; closing a row means improving the "
        "diagnostic, not the test.\n",
        f"**{len(misses)} of {s['totals']['diag_total']} cases fall short of "
        "the ideal.**\n",
        "| Case | Ideal (`.expected`) | Actual |",
        "|---|---|---|",
    ]
    for m in misses:
        want = (m["want"] or "—").replace("\n", " ").replace("|", "\\|")
        got = (m["got"] or "—").replace("|", "\\|")
        out.append(f"| `{m['case']}` | {want} | {got} |")
    return "\n".join(out) + "\n"


def write_artifacts(s: dict) -> None:
    with open(JSON_PATH, "w") as fh:
        fh.write(dumps(s))
    with open(MD_PATH, "w") as fh:
        fh.write(render_markdown(s))


def cmd_check(cur: dict) -> int:
    # Floor enforcement (the committed bounds). A measured metric below its
    # floor is red: build_ok is a strict ratchet, diag_ok is relaxable only
    # via a justified edit to metric-floors.toml. (read_floors aborts red if
    # the floors file is missing/malformed — a tooling failure.)
    floors = read_floors()
    ab, ad = cur["totals"]["build_ok"], cur["totals"]["diag_ok"]
    if ab < floors["build_ok_min"]:
        print(f"corpus-status: REGRESSION: build_ok {ab} < floor {floors['build_ok_min']} — "
              "a previously-building example no longer builds", file=sys.stderr)
        return 1
    if ad < floors["diag_ok_min"]:
        print(f"corpus-status: REGRESSION: diag_ok {ad} < floor {floors['diag_ok_min']} — "
              "fewer negative cases reproduce their diagnostic. Fix the case(s), or, if a "
              "diagnostic legitimately changed, lower diag_ok_min in metric-floors.toml with "
              "a justification.", file=sys.stderr)
        return 1
    # Snapshot freshness (the generated display must reflect reality).
    try:
        committed = json.load(open(JSON_PATH))
    except FileNotFoundError:
        print(f"corpus-status: {JSON_PATH} missing — run scripts/corpus_status.py", file=sys.stderr)
        return 1
    if dumps(cur).strip() != dumps(committed).strip():
        print(f"corpus-status: snapshot stale (build_ok={ab}, diag_ok={ad}) — "
              "run scripts/corpus_status.py and commit the result", file=sys.stderr)
        return 1
    try:
        md = open(MD_PATH).read()
    except FileNotFoundError:
        md = None
    if md != render_markdown(cur):
        print(f"corpus-status: {MD_PATH} stale — run scripts/corpus_status.py and commit", file=sys.stderr)
        return 1
    print(f"corpus-status: up to date (build_ok {ab}/{cur['totals']['total']}, "
          f"diag_ok {ad}/{cur['totals']['diag_total']})")
    return 0


def cmd_history() -> int:
    log = subprocess.check_output(
        ["git", "log", "--reverse", "--format=%H%x09%cI%x09%s", "--", JSON_PATH],
        text=True,
    )
    for ln in log.splitlines():
        if not ln.strip():
            continue
        sha, date, subject = ln.split("\t", 2)
        try:
            blob = subprocess.check_output(["git", "show", f"{sha}:{JSON_PATH}"], text=True)
            s = json.loads(blob)
            # Skip blobs from an incompatible (future) schema rather
            # than mis-reading them; tolerate any missing key.
            if s.get("schema_version") != SCHEMA_VERSION:
                continue
            t = s["totals"]
        except (subprocess.CalledProcessError, json.JSONDecodeError, KeyError):
            continue
        print(json.dumps({
            "date": date, "commit": sha[:12], "subject": subject,
            "total": t["total"], "build_ok": t["build_ok"],
            "diag_ok": t.get("diag_ok"), "diag_total": t.get("diag_total"),
            "emit_fail": t["emit_fail"], "sema_fail": t["sema_fail"],
            "parse_fail": t["parse_fail"],
        }, ensure_ascii=False))
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="tide example conformance scoreboard")
    ap.add_argument("--check", action="store_true", help="fail if snapshot stale or build_ok regressed")
    ap.add_argument("--history", action="store_true", help="emit the trend (JSONL) from git history")
    args = ap.parse_args()

    os.chdir(repo_root())

    if args.history:
        return cmd_history()

    cur = collect()
    if args.check:
        return cmd_check(cur)
    write_artifacts(cur)
    t = cur["totals"]
    print(f"corpus-status: wrote {JSON_PATH} + {MD_PATH} ({t['build_ok']}/{t['total']} build)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
