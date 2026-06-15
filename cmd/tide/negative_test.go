package main

// Corpus negative-case harness (RFC-0004). For each `[[error]]` entry in an
// example's example.toml, apply its marker-anchored unified-diff patch to the
// base program, compile the result, and assert the emitted diagnostics match
// (tolerantly): the expected code(s) as an unordered subset, the rejecting
// stage, and the message substring from the paired sidecar. A patch that no
// longer applies is a hard failure, not a skip — the signal that the base
// moved and the case must be regenerated.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type negCase struct {
	name    string // example dir name, for subtest labels
	dir     string // absolute example dir
	entry   string // base program filename
	patch   string // patch path, relative to dir
	expect  []string
	stage   string
	matches string // expected-message sidecar, relative to dir
}

var codeRe = regexp.MustCompile(`error\[(E\d+)\]`)

// stageOf mirrors the classification in scripts/corpus_status.py.
var parseCodes = map[string]bool{
	"E0101": true, "E0102": true, "E0107": true, "E0109": true, "E0110": true, "E0111": true, "E0112": true,
}
var emitCodes = map[string]bool{"E0801": true, "E0802": true, "E0803": true}

func stageOf(code string) string {
	switch {
	case parseCodes[code]:
		return "parse"
	case emitCodes[code]:
		return "emit"
	default:
		return "sema"
	}
}

func tomlScalar(line string) string {
	_, v, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if j := strings.Index(v[1:], `"`); j >= 0 {
			return v[1 : 1+j]
		}
	}
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' || r == '#' }) {
		return strings.Trim(f, `"`)
	}
	return ""
}

func tomlArray(line string) []string {
	_, v, _ := strings.Cut(line, "=")
	if i := strings.Index(v, "]"); i >= 0 {
		v = v[:i+1] // drop any trailing comment after the array
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(v, -1) {
		out = append(out, m[1])
	}
	return out
}

// parseNegCases extracts the [[error]] entries from one example.toml.
func parseNegCases(t *testing.T, manifest string) []negCase {
	t.Helper()
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read %s: %v", manifest, err)
	}
	dir := filepath.Dir(manifest)
	var entry string
	var cases []negCase
	var cur *negCase
	flush := func() {
		if cur != nil {
			cases = append(cases, *cur)
			cur = nil
		}
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "[[error]]":
			flush()
			cur = &negCase{dir: dir, name: filepath.Base(dir)}
		case strings.HasPrefix(line, "entry"):
			entry = tomlScalar(line) // top-level key; captured position-independently
		case strings.HasPrefix(line, "["):
			flush()
		case cur == nil:
			// other top-level keys are not needed by the harness
		case strings.HasPrefix(line, "patch"):
			cur.patch = tomlScalar(line)
		case strings.HasPrefix(line, "expect"):
			cur.expect = tomlArray(line)
		case strings.HasPrefix(line, "stage"):
			cur.stage = tomlScalar(line)
		case strings.HasPrefix(line, "matches"):
			cur.matches = tomlScalar(line)
		}
	}
	flush()
	for i := range cases {
		cases[i].entry = entry
	}
	return cases
}

func TestCorpusNegativeCases(t *testing.T) {
	root := projectRoot(t)
	manifests, err := filepath.Glob(filepath.Join(root, "examples", "*", "*", "example.toml"))
	if err != nil {
		t.Fatalf("glob manifests: %v", err)
	}
	var cases []negCase
	for _, m := range manifests {
		cases = append(cases, parseNegCases(t, m)...)
	}
	if len(cases) == 0 {
		t.Skip("no corpus negative cases declared yet")
	}
	for _, c := range cases {
		c := c
		t.Run(c.name+"/"+filepath.Base(c.patch), func(t *testing.T) {
			runNegativeCase(t, c)
		})
	}
}

func runNegativeCase(t *testing.T, c negCase) {
	if c.patch == "" || len(c.expect) == 0 || c.entry == "" {
		t.Fatalf("malformed negative case in %s (patch/expect/entry required)", c.dir)
	}
	tmp := t.TempDir()
	src, err := os.ReadFile(filepath.Join(c.dir, c.entry))
	if err != nil {
		t.Fatalf("read base program: %v", err)
	}
	patched := filepath.Join(tmp, c.entry)
	if err := os.WriteFile(patched, src, 0o644); err != nil {
		t.Fatalf("stage base program: %v", err)
	}

	// Apply the patch. A patch that no longer applies is a hard failure.
	ap := exec.Command("patch", "-p0", "-s", "-d", tmp, "-i", filepath.Join(c.dir, c.patch))
	if out, err := ap.CombinedOutput(); err != nil {
		t.Fatalf("patch no longer applies (%s/%s) — regenerate this negative case: %v\n%s",
			c.name, c.patch, err, out)
	}

	stdout, stderr, exit := runTide(t, "build", "-o", filepath.Join(tmp, "out.bin"), patched)
	combined := stdout + stderr
	if exit == 0 {
		t.Fatalf("%s/%s: expected a diagnostic but the patched program built cleanly", c.name, c.patch)
	}

	emitted := map[string]bool{}
	for _, m := range codeRe.FindAllStringSubmatch(combined, -1) {
		emitted[m[1]] = true
	}
	// Unordered subset match on codes; each expected code's stage must match.
	for _, code := range c.expect {
		if !emitted[code] {
			t.Errorf("%s/%s: expected diagnostic %s not emitted.\nGot:\n%s", c.name, c.patch, code, combined)
		}
		if c.stage != "" && stageOf(code) != c.stage {
			t.Errorf("%s/%s: %s belongs to stage %q but manifest declares %q",
				c.name, c.patch, code, stageOf(code), c.stage)
		}
	}
	// Tolerant message match: every non-empty sidecar line is a substring.
	if c.matches != "" {
		want, err := os.ReadFile(filepath.Join(c.dir, c.matches))
		if err != nil {
			t.Fatalf("read expected-message sidecar: %v", err)
		}
		for _, ln := range strings.Split(strings.TrimSpace(string(want)), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" && !strings.Contains(combined, ln) {
				t.Errorf("%s/%s: diagnostic missing expected text %q.\nGot:\n%s", c.name, c.patch, ln, combined)
			}
		}
	}
}

func TestNegCaseTOMLLite(t *testing.T) {
	if got := tomlScalar(`patch   = "errors/x.patch"   # comment`); got != "errors/x.patch" {
		t.Errorf("tomlScalar quoted = %q", got)
	}
	if got := tomlScalar(`stage = parse`); got != "parse" {
		t.Errorf("tomlScalar bare = %q", got)
	}
	if got := tomlArray(`expect = ["E0112", "E0201"]`); len(got) != 2 || got[0] != "E0112" || got[1] != "E0201" {
		t.Errorf("tomlArray = %v", got)
	}
	if got := tomlArray(`expect = ["E0112"]   # not "E0201"`); len(got) != 1 || got[0] != "E0112" {
		t.Errorf("tomlArray must ignore commented quotes, got %v", got)
	}
	for code, want := range map[string]string{"E0112": "parse", "E0801": "emit", "E0203": "sema"} {
		if got := stageOf(code); got != want {
			t.Errorf("stageOf(%s) = %q, want %q", code, got, want)
		}
	}
}
