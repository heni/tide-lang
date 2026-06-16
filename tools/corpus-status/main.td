import fmt
import os
import strings
import sort
import json
import strconv

// corpus-status — the corpus conformance scoreboard (see README.md).
// Measures two metrics over the example corpus: `build_ok` (how far each
// example gets through the build pipeline) and `diag_ok` (whether each
// negative case reproduces its ideal diagnostic), renders the JSON snapshot
// + STATUS.md, and offers `--check` (ratchet) and `--history` (trend).

// Diagnostics emitted before a file parses; any other code means the file
// parsed and a later stage rejected it.
func isParseCode(code: string): bool {
  return (code == "E0101" || code == "E0102" || code == "E0107"
    || code == "E0109" || code == "E0110" || code == "E0111" || code == "E0112")
}

func isEmitCode(code: string): bool {
  return code == "E0801" || code == "E0802" || code == "E0803"
}

func stageOf(code: string): string {
  if isParseCode(code) { return "parse" }
  if isEmitCode(code) { return "emit" }
  return "sema"
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

// --- diagnostic parsing ---

// diagPos returns the index of a tide diagnostic marker in line, or -1.
func diagPos(line: string): int {
  let i = indexOf(line, "error[")
  if i >= 0 { return i }
  return indexOf(line, "internal[")
}

// firstDiagLine returns the first diagnostic line sliced from its marker
// (dropping the leading "<path>:<line>:<col>: " position prefix), or "".
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

// --- negative cases: the "correct error messages" metric (diag_ok) ---
//
// Each [[error]] in an example.toml is a patch that injects a mistake plus the
// diagnostic it must provoke. diag_ok counts the cases that reproduce their
// expected diagnostic (code set ⊆ emitted, stage matches, message substrings
// present). A case that fails to reproduce is a *penalty* on diag_ok — never a
// tooling error. A patch that no longer applies, or a compiler/subprocess that
// cannot be run, *is* a tooling error and aborts the whole run.

type NegCase = {
  dir: string
  entry: string
  patch: string
  expect: []string
  stage: string
  matches: string
}

type EvalResult = {
  diag_ok: int
  diag_total: int
  misses: []Miss
}

func containsStr(xs: []string, x: string): bool {
  for e in xs {
    if e == x { return true }
  }
  return false
}

func trimQuotes(s: string): string {
  return strings.trimSuffix(strings.trimPrefix(s, "\""), "\"")
}

// tomlScalar extracts a TOML-lite string value: the part after the first
// `=`, the inner of a leading quoted run if present, else the pre-`#` text
// with surrounding quotes stripped.
func tomlScalar(line: string): string {
  let eq = indexOf(line, "=")
  if eq < 0 { return "" }
  var v = strings.trimSpace(line[eq + 1:])
  if strings.hasPrefix(v, "\"") {
    let rest = v[1:]
    let j = indexOf(rest, "\"")
    if j >= 0 { return rest[:j] }
  }
  let h = indexOf(v, "#")
  if h >= 0 { v = v[:h] }
  return trimQuotes(strings.trimSpace(v))
}

// tomlCodes extracts the quoted strings of an array value: everything after
// `=` up to the first `]`, then each `"..."`.
func tomlCodes(line: string): []string {
  let eq = indexOf(line, "=")
  if eq < 0 { return []string{} }
  var v = line[eq + 1:]
  let rb = indexOf(v, "]")
  if rb >= 0 { v = v[:rb] }
  var out = []string{}
  var rest = v
  while true {
    let q1 = indexOf(rest, "\"")
    if q1 < 0 { break }
    rest = rest[q1 + 1:]
    let q2 = indexOf(rest, "\"")
    if q2 < 0 { break }
    out = out.push(rest[:q2])
    rest = rest[q2 + 1:]
  }
  return out
}

func readFileOrEmpty(path: string): string {
  match os.readFile(path) {
    Ok(data) => return strings.fromBytes(data),
    Err(_) => return "",
  }
}

// parseNegCases extracts the [[error]] entries of one example.toml. `entry`
// is the manifest's top-level entry, applied to every case (resolved in a
// first pass so it is order-independent).
func parseNegCases(manifest: string, text: string): []NegCase {
  let dir = pathDir(manifest)
  var entry = ""
  for raw in strings.split(text, "\n") {
    let line = strings.trimSpace(raw)
    if strings.hasPrefix(line, "entry") {
      entry = tomlScalar(line)
    }
  }
  var cases = []NegCase{}
  var cur = NegCase{ dir: dir, entry: entry, patch: "", expect: []string{}, stage: "", matches: "" }
  var inCase = false
  for raw in strings.split(text, "\n") {
    let line = strings.trimSpace(raw)
    if line == "[[error]]" {
      if inCase { cases = cases.push(cur) }
      cur = NegCase{ dir: dir, entry: entry, patch: "", expect: []string{}, stage: "", matches: "" }
      inCase = true
    } else if strings.hasPrefix(line, "[") {
      if inCase { cases = cases.push(cur) }
      inCase = false
    } else if inCase {
      if strings.hasPrefix(line, "patch") {
        cur.patch = tomlScalar(line)
      } else if strings.hasPrefix(line, "expect") {
        cur.expect = tomlCodes(line)
      } else if strings.hasPrefix(line, "stage") {
        cur.stage = tomlScalar(line)
      } else if strings.hasPrefix(line, "matches") {
        cur.matches = tomlScalar(line)
      }
    }
  }
  if inCase { cases = cases.push(cur) }
  return cases
}

// emittedCodes returns the E-codes appearing as `error[E…]` in compiler
// output (a findall over the combined stream).
func emittedCodes(re: Regexp, combined: string): []string {
  var out = []string{}
  for m in re.findAll(combined, -1) {
    if m.len() > 7 {
      // strip the leading "error[" (6 chars) and the trailing "]".
      out = out.push(m[6:m.len() - 1])
    }
  }
  return out
}

func copyFile(src: string, dst: string) {
  match os.readFile(src) {
    Ok(data) => {
      match writeFile(dst, data, 420) {
        Ok(_) => fmt.print(""),
        Err(e) => {
          fmt.println("corpus-status: stage write failed:", dst, e)
          os.exit(1)
        },
      }
    },
    Err(e) => {
      fmt.println("corpus-status: stage read failed:", src, e)
      os.exit(1)
    },
  }
}

func lessMiss(a: Miss, b: Miss): bool {
  return a.case < b.case
}

// mustCompile compiles a known-good regexp, aborting on the impossible
// failure (an opaque handle cannot be zero-constructed, so the value must
// come from the extern call).
func mustCompile(expr: string): Regexp {
  match compileRegexp(expr) {
    Ok(c) => return c,
    Err(e) => {
      fmt.println("corpus-status: internal regexp failed:", e)
      os.exit(1)
    },
  }
}

func evalNegatives(tide: string): EvalResult {
  let rx = mustCompile("error\\[E[0-9]+\\]")
  let listed = runOk("git", ["ls-files", "examples"])
  var manifests = []string{}
  for ln in strings.split(listed, "\n") {
    if pathBase(ln) == "example.toml" {
      manifests = manifests.push(ln)
    }
  }
  manifests = sort.sorted(manifests, lessStr)

  var work = ""
  match mkdirTemp("", "corpus-neg-") {
    Ok(d) => work = d,
    Err(e) => {
      fmt.println("corpus-status: mkdirTemp failed:", e)
      os.exit(1)
    },
  }

  var diagOk = 0
  var diagTotal = 0
  var misses = []Miss{}
  for mf in manifests {
    let text = readFileOrEmpty(mf)
    for c in parseNegCases(mf, text) {
      if c.patch == "" || c.expect.len() == 0 || c.entry == "" {
        continue
      }
      diagTotal = diagTotal + 1
      let caseLabel = c.dir + "/" + c.patch
      var tmp = ""
      match mkdirTemp(work, "") {
        Ok(d) => tmp = d,
        Err(e) => {
          fmt.println("corpus-status: mkdirTemp failed:", e)
          os.exit(1)
        },
      }
      let staged = pathJoin(tmp, c.entry)
      copyFile(pathJoin(c.dir, c.entry), staged)
      var patchfile = ""
      match pathAbs(pathJoin(c.dir, c.patch)) {
        Ok(p) => patchfile = p,
        Err(e) => {
          fmt.println("corpus-status: abs path failed:", e)
          os.exit(1)
        },
      }
      // Absolute -i: `patch -d tmp` resolves a relative -i against tmp.
      let ap = run("patch", ["-p0", "-s", "-d", tmp, "-i", patchfile])
      if ap.code() != 0 {
        fmt.println("corpus-status: patch no longer applies", caseLabel, "— regenerate this case:")
        fmt.println(ap.out())
        os.exit(1)
      }
      let r = run(tide, ["build", "-o", pathJoin(tmp, "out.bin"), staged])
      let combined = r.out()
      var want = ""
      if c.matches != "" {
        want = strings.trimSpace(readFileOrEmpty(pathJoin(c.dir, c.matches)))
      }
      var got = firstDiagLine(combined)
      if got == "" {
        if r.code() == 0 {
          got = "(built clean — no diagnostic)"
        } else {
          got = "(no diagnostic)"
        }
      }
      if r.code() == 0 {
        misses = misses.push(Miss{ case: caseLabel, want: want, got: got })
        continue
      }
      let emitted = emittedCodes(rx, combined)
      var ok = emitted.len() > 0
      for code in c.expect {
        if !containsStr(emitted, code) {
          ok = false
        }
        if c.stage != "" && stageOf(code) != c.stage {
          ok = false
        }
      }
      if ok && want != "" {
        for wl in strings.split(want, "\n") {
          let wls = strings.trimSpace(wl)
          if wls != "" && !strings.contains(combined, wls) {
            ok = false
          }
        }
      }
      if ok {
        diagOk = diagOk + 1
      } else {
        misses = misses.push(Miss{ case: caseLabel, want: want, got: got })
      }
    }
  }
  match removeAll(work) {
    Ok(_) => fmt.print(""),
    Err(_) => fmt.print(""),
  }
  return EvalResult{ diag_ok: diagOk, diag_total: diagTotal, misses: sort.sorted(misses, lessMiss) }
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
  let ev = evalNegatives(tide)
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
      diag_ok: ev.diag_ok,
      diag_total: ev.diag_total,
    },
    files: rows,
    diag_misses: ev.misses,
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
  out = out + "Generated from `examples/auto-status.json` by the `corpus-status` tool (`tools/corpus-status`) — do not edit by hand. Build and run the tool to refresh.\n\n"
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

func jsonPath(): string {
  return "examples/auto-status.json"
}

func mdPath(): string {
  return "examples/STATUS.md"
}

// statusJson renders the deterministic snapshot (json.dumps(indent=2) shape,
// trailing newline) shared by the write and --check paths.
func statusJson(s: Status): string {
  match json.serializeIndent(s, "", "  ") {
    Ok(b) => return strings.fromBytes(b) + "\n",
    Err(e) => {
      fmt.println("corpus-status: JSON serialise failed:", e)
      os.exit(1)
    },
  }
}

func writeArtifacts(s: Status) {
  writeOrDie(jsonPath(), statusJson(s))
  writeOrDie(mdPath(), renderMarkdown(s))
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

// --- --check: floor enforcement + snapshot freshness ---

func cmdCheck(cur: Status): int {
  let floors = readFloors()
  let ab = cur.totals.build_ok
  let ad = cur.totals.diag_ok
  if ab < floors.build_ok_min {
    fmt.println(fmt.sprintf("corpus-status: REGRESSION: build_ok %d < floor %d — a previously-building example no longer builds", ab, floors.build_ok_min))
    return 1
  }
  if ad < floors.diag_ok_min {
    fmt.println(fmt.sprintf("corpus-status: REGRESSION: diag_ok %d < floor %d — fewer negative cases reproduce their diagnostic. Fix the case(s), or, if a diagnostic legitimately changed, lower diag_ok_min in metric-floors.toml with a justification.", ad, floors.diag_ok_min))
    return 1
  }
  let committed = readFileOrEmpty(jsonPath())
  if committed == "" {
    fmt.println("corpus-status:", jsonPath(), "missing — run corpus-status")
    return 1
  }
  if strings.trimSpace(statusJson(cur)) != strings.trimSpace(committed) {
    fmt.println(fmt.sprintf("corpus-status: snapshot stale (build_ok=%d, diag_ok=%d) — run corpus-status and commit the result", ab, ad))
    return 1
  }
  if readFileOrEmpty(mdPath()) != renderMarkdown(cur) {
    fmt.println("corpus-status:", mdPath(), "stale — run corpus-status and commit")
    return 1
  }
  fmt.println(fmt.sprintf("corpus-status: up to date (build_ok %d/%d, diag_ok %d/%d)", ab, cur.totals.total, ad, cur.totals.diag_total))
  return 0
}

// --- --history: the trend (JSONL) reconstructed from git ---

type HistTotals = {
  total: int
  build_ok: int
  diag_ok: int
  diag_total: int
  emit_fail: int
  sema_fail: int
  parse_fail: int
}

type HistStatus = {
  schema_version: int
  totals: HistTotals
}

// jsonStrLit renders a JSON string literal the way Python's json.dumps does
// with ensure_ascii=False: escape `\` and `"` (and the control chars that
// occur in this data — none, git %s is single-line), but leave `<`/`>`/`&`
// raw. Go's json.Marshal HTML-escapes those, which would diverge from the
// Python tool on subjects like `fmt.scan<T>` — so we build the line by hand.
func jsonStrLit(s: string): string {
  let a = strings.replace(s, "\\", "\\\\", -1)
  let b = strings.replace(a, "\"", "\\\"", -1)
  return "\"" + b + "\""
}

// historyLine renders one JSONL row with the Python tool's exact field order
// and `", "` / `": "` separators.
func historyLine(date: string, commit: string, subject: string, t: HistTotals): string {
  return ("{"
    + "\"date\": " + jsonStrLit(date)
    + ", \"commit\": " + jsonStrLit(commit)
    + ", \"subject\": " + jsonStrLit(subject)
    + ", \"total\": " + strconv.itoa(t.total)
    + ", \"build_ok\": " + strconv.itoa(t.build_ok)
    + ", \"diag_ok\": " + strconv.itoa(t.diag_ok)
    + ", \"diag_total\": " + strconv.itoa(t.diag_total)
    + ", \"emit_fail\": " + strconv.itoa(t.emit_fail)
    + ", \"sema_fail\": " + strconv.itoa(t.sema_fail)
    + ", \"parse_fail\": " + strconv.itoa(t.parse_fail)
    + "}")
}

func cmdHistory() {
  let log = runOk("git", ["log", "--reverse", "--format=%H%x09%cI%x09%s", "--", jsonPath()])
  for ln in strings.split(log, "\n") {
    let line = strings.trimSpace(ln)
    if line == "" { continue }
    let parts = splitN(line, "\t", 3)
    if parts.len() < 3 { continue }
    let sha = parts[0]
    let blob = run("git", ["show", sha + ":" + jsonPath()])
    if blob.code() != 0 { continue }
    var hs = HistStatus{
      schema_version: 0,
      totals: HistTotals{ total: 0, build_ok: 0, diag_ok: 0, diag_total: 0, emit_fail: 0, sema_fail: 0, parse_fail: 0 },
    }
    var parsed = false
    match json.parse<HistStatus>(toBytes(blob.out())) {
      Ok(s) => {
        hs = s
        parsed = true
      },
      Err(_) => parsed = false,
    }
    // Skip blobs from an incompatible (future/older) schema rather than
    // mis-reading them.
    if !parsed || hs.schema_version != 2 { continue }
    fmt.println(historyLine(parts[1], sha[:12], parts[2], hs.totals))
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
  let args = os.args
  var mode = "write"
  if args.len() > 1 {
    if args[1] == "--check" {
      mode = "check"
    } else if args[1] == "--history" {
      mode = "history"
    } else {
      fmt.println("usage: corpus-status [--check|--history]")
      os.exit(2)
    }
  }
  if mode == "history" {
    cmdHistory()
    os.exit(0)
  }
  let status = collect()
  if mode == "check" {
    os.exit(cmdCheck(status))
  }
  writeArtifacts(status)
  let t = status.totals
  fmt.println(fmt.sprintf("corpus-status: wrote examples/auto-status.json + examples/STATUS.md (%d/%d build)", t.build_ok, t.total))
}
