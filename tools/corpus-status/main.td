import fmt
import os
import strings
import sort
import json
import strconv

// corpus-status (Tide port of scripts/corpus_status.py). This slice
// measures `build_ok`: how far each corpus example gets through the build
// pipeline. The `diag_ok` negative-case metric and the --check / --history
// subcommands land in following slices. See TODO.md.

// Diagnostics emitted before a file parses; any other code means the file
// parsed and a later stage rejected it (mirrors corpus_status.py).
func isParseCode(code: string): bool {
  return (code == "E0101" || code == "E0102" || code == "E0107"
    || code == "E0109" || code == "E0110" || code == "E0111" || code == "E0112")
}

func isEmitCode(code: string): bool {
  return code == "E0801" || code == "E0802" || code == "E0803"
}

// --- records ---

type Row = {
  path: string
  stage: string
  blocker: string
}

type Miss = {
  case: string
  want: string
  got: string
}

type Totals = {
  total: int
  parse_fail: int
  sema_fail: int
  emit_fail: int
  build_ok: int
  diag_ok: int
  diag_total: int
}

type Status = {
  schema_version: int
  totals: Totals
  files: []Row
  diag_misses: []Miss
}

// --- subprocess helpers ---

// runOk runs a command that must succeed; aborts (tooling failure) otherwise.
func runOk(name: string, args: []string): string {
  let r = run(name, args)
  if r.code() != 0 {
    fmt.println("corpus-status: command failed:", name, "exit", r.code())
    fmt.println(r.out())
    os.exit(1)
  }
  return strings.trimSpace(r.out())
}

// --- diagnostic parsing (mirrors corpus_status.py) ---

// diagPos returns the index of a tide diagnostic marker in line, or -1.
func diagPos(line: string): int {
  let i = indexOf(line, "error[")
  if i >= 0 { return i }
  return indexOf(line, "internal[")
}

// firstDiagLine returns the first diagnostic line (from its marker, with the
// leading "<path>:<line>:<col>: " position stripped), or "".
func firstDiagLine(out: string): string {
  for ln in strings.split(out, "\n") {
    let k = diagPos(ln)
    if k >= 0 { return strings.trimSpace(ln[k:]) }
  }
  return ""
}

// errorCode extracts the diagnostic code (between the first `[` at/after the
// marker and the next `]`), or "".
func errorCode(line: string): string {
  let i = diagPos(line)
  if i < 0 { return "" }
  let rest0 = line[i:]
  let lb = indexOf(rest0, "[")
  if lb < 0 { return "" }
  let rest = rest0[lb + 1:]
  let rb = indexOf(rest, "]")
  if rb < 0 { return "" }
  return rest[:rb]
}

// --- classify one file ---

func classify(tide: string, file: string, out: string): Row {
  let r = run(tide, ["build", "-o", out, file])
  if r.code() == 0 {
    return Row{ path: file, stage: "build", blocker: "" }
  }
  let combined = r.out()
  let diag = firstDiagLine(combined)
  let code = errorCode(diag)
  if isParseCode(code) {
    return Row{ path: file, stage: "parse", blocker: diag }
  }
  if isEmitCode(code) {
    return Row{ path: file, stage: "emit", blocker: diag }
  }
  if strings.contains(combined, "go build failed") {
    // Raw Go-toolchain output is non-deterministic; record a stable blocker.
    return Row{ path: file, stage: "emit", blocker: "go build failed" }
  }
  if code != "" {
    return Row{ path: file, stage: "sema", blocker: diag }
  }
  var blocker = diag
  if blocker == "" { blocker = "unknown failure" }
  return Row{ path: file, stage: "emit", blocker: blocker }
}

// --- corpus enumeration ---

func lessStr(a: string, b: string): bool {
  return a < b
}

// corpusFiles returns the git-tracked .td files under examples/ + user_tests/,
// sorted — so the snapshot reflects the committed suite (untracked scratch
// .td files don't perturb the metric).
func corpusFiles(): []string {
  let listed = runOk("git", ["ls-files", "examples", "user_tests"])
  var files = []string{}
  for ln in strings.split(listed, "\n") {
    if strings.hasSuffix(ln, ".td") {
      files = files.push(ln)
    }
  }
  return sort.sorted(files, lessStr)
}

func buildTide(tmp: string): string {
  let binpath = pathJoin(tmp, "tide")
  let r = run("go", ["build", "-o", binpath, "./cmd/tide"])
  if r.code() != 0 {
    fmt.println("corpus-status: building tide failed:")
    fmt.println(r.out())
    os.exit(1)
  }
  return binpath
}

// --- collect ---

func collect(): Status {
  var tmp = ""
  match mkdirTemp("", "corpus-status-") {
    Ok(d) => tmp = d,
    Err(e) => {
      fmt.println("corpus-status: mkdirTemp failed:", e)
      os.exit(1)
    },
  }
  let tide = buildTide(tmp)
  let outp = pathJoin(tmp, "out")
  let files = corpusFiles()
  var rows = []Row{}
  var nparse = 0
  var nsema = 0
  var nemit = 0
  var nbuild = 0
  for f in files {
    let row = classify(tide, f, outp)
    rows = rows.push(row)
    if row.stage == "build" {
      nbuild = nbuild + 1
    } else if row.stage == "emit" {
      nemit = nemit + 1
    } else if row.stage == "sema" {
      nsema = nsema + 1
    } else {
      nparse = nparse + 1
    }
  }
  match removeAll(tmp) {
    Ok(_) => fmt.print(""),
    Err(_) => fmt.print(""),
  }
  return Status{
    schema_version: 2,
    totals: Totals{
      total: files.len(),
      parse_fail: nparse,
      sema_fail: nsema,
      emit_fail: nemit,
      build_ok: nbuild,
      diag_ok: 0,
      diag_total: 0,
    },
    files: rows,
    diag_misses: []Miss{},
  }
}

// --- floors ---

type Floors = {
  build_ok_min: int
  diag_ok_min: int
}

func parseFloorLine(line: string, key: string): int {
  let t = strings.trimSpace(line)
  if !strings.hasPrefix(t, key) {
    return -1
  }
  let parts = splitN(t, "=", 2)
  if parts.len() < 2 {
    return -1
  }
  // value may carry a trailing `# comment`; take the leading integer field.
  let rhs = strings.trimSpace(parts[1])
  let fields = strings.split(rhs, " ")
  match atoiSafe(fields[0]) {
    Ok(n) => return n,
    Err(_) => return -1,
  }
}

func atoiSafe(s: string): Result<int, error> {
  return strconv.atoi(strings.trimSpace(s))
}

func readFloors(): Floors {
  var text = ""
  match os.readFile("examples/metric-floors.toml") {
    Ok(data) => text = strings.fromBytes(data),
    Err(_) => {
      fmt.println("corpus-status: examples/metric-floors.toml missing — the metric floors are unset")
      os.exit(1)
    },
  }
  var bmin = -1
  var dmin = -1
  for line in strings.split(text, "\n") {
    let b = parseFloorLine(line, "build_ok_min")
    if b >= 0 { bmin = b }
    let d = parseFloorLine(line, "diag_ok_min")
    if d >= 0 { dmin = d }
  }
  if bmin < 0 || dmin < 0 {
    fmt.println("corpus-status: examples/metric-floors.toml missing or malformed floor keys")
    os.exit(1)
  }
  return Floors{ build_ok_min: bmin, diag_ok_min: dmin }
}

// --- markdown render ---

func stageOrder(s: string): int {
  if s == "build" { return 0 }
  if s == "emit" { return 1 }
  if s == "sema" { return 2 }
  return 3
}

func lessRow(x: Row, y: Row): bool {
  if stageOrder(x.stage) != stageOrder(y.stage) {
    return stageOrder(x.stage) < stageOrder(y.stage)
  }
  return x.path < y.path
}

func mdEscape(s: string): string {
  return strings.replace(s, "|", "\\|", -1)
}

func renderMarkdown(s: Status): string {
  let t = s.totals
  let floors = readFloors()
  var out = ""
  out = out + "# Example conformance status\n\n"
  out = out + "Generated by `scripts/corpus_status.py` from `examples/auto-status.json` — do not edit by hand. Run `scripts/corpus_status.py` to refresh.\n\n"
  out = out + "Two tracked metrics, each with a CI-enforced floor in `metric-floors.toml`:\n\n"
  out = out + fmt.sprintf("- **build_ok — %d / %d examples build end-to-end** (floor %d).\n", t.build_ok, t.total, floors.build_ok_min)
  out = out + fmt.sprintf("- **diag_ok — %d / %d negative cases produce their expected diagnostic** (floor %d).\n\n", t.diag_ok, t.diag_total, floors.diag_ok_min)
  out = out + "| Stage reached | Count |\n"
  out = out + "|---|---|\n"
  out = out + fmt.sprintf("| ✅ build (full pipeline) | %d |\n", t.build_ok)
  out = out + fmt.sprintf("| emit / codegen fail | %d |\n", t.emit_fail)
  out = out + fmt.sprintf("| sema fail | %d |\n", t.sema_fail)
  out = out + fmt.sprintf("| parse fail | %d |\n", t.parse_fail)
  out = out + "\n## Per-example\n\n"
  out = out + "| Example | Stage | First blocker |\n"
  out = out + "|---|---|---|\n"
  let rows = sort.sorted(s.files, lessRow)
  for f in rows {
    var blk = f.blocker
    if blk == "" { blk = "—" }
    out = out + fmt.sprintf("| `%s` | %s | %s |\n", f.path, f.stage, mdEscape(blk))
  }
  out = out + "\n## Diagnostic-quality gaps\n\n"
  out = out + "Negative cases whose `.expected` records the **ideal** user-facing diagnostic that the compiler does not yet emit (e.g. a parser message still leaking internal token-kind names). This is the backlog the `diag_ok` metric grows toward; closing a row means improving the diagnostic, not the test.\n\n"
  out = out + fmt.sprintf("**%d of %d cases fall short of the ideal.**\n\n", s.diag_misses.len(), t.diag_total)
  out = out + "| Case | Ideal (`.expected`) | Actual |\n"
  out = out + "|---|---|---|\n"
  for m in s.diag_misses {
    var want = m.want
    if want == "" { want = "—" }
    var got = m.got
    if got == "" { got = "—" }
    out = out + fmt.sprintf("| `%s` | %s | %s |\n", m.case, mdEscape(strings.replace(want, "\n", " ", -1)), mdEscape(got))
  }
  return out
}

// --- write artifacts ---

func writeArtifacts(s: Status) {
  var jsonStr = ""
  match json.serializeIndent(s, "", "  ") {
    Ok(b) => jsonStr = strings.fromBytes(b) + "\n",
    Err(e) => {
      fmt.println("corpus-status: JSON serialise failed:", e)
      os.exit(1)
    },
  }
  writeOrDie("examples/auto-status.json", jsonStr)
  writeOrDie("examples/STATUS.md", renderMarkdown(s))
}

func writeOrDie(path: string, content: string) {
  match writeFile(path, toBytes(content), 420) {
    Ok(_) => fmt.print(""),
    Err(e) => {
      fmt.println("corpus-status: write", path, "failed:", e)
      os.exit(1)
    },
  }
}

func main() {
  // Run from the repo root so relative paths + git resolve regardless of cwd.
  let root = runOk("git", ["rev-parse", "--show-toplevel"])
  match chdir(root) {
    Ok(_) => fmt.print(""),
    Err(e) => {
      fmt.println("corpus-status: chdir failed:", e)
      os.exit(1)
    },
  }
  let status = collect()
  writeArtifacts(status)
  let t = status.totals
  fmt.println(fmt.sprintf("corpus-status: wrote examples/auto-status.json + examples/STATUS.md (%d/%d build)", t.build_ok, t.total))
}
