// Why: the tooling commands are scripted by users and editors, so their flags,
// streams and exit statuses must hold.
// What: tests of mk fmt, mk test's --sim and --prop, mk explain, mk fix (text
// and --json), mk apidiff, mk query, and mk build's bench and --emit-ll flags.
// How: writes small files to a temporary directory, runs each command
// through mk() and checks status, stdout and stderr.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestFmtCommand: --check lists the unformatted file and exits 1 without
// writing; a file that does not parse is reported and left alone; mk fmt
// rewrites; a second --check is clean apart from the syntax error.
func TestFmtCommand(t *testing.T) {
	dir := t.TempDir()
	ugly := "A( ) {\n\tx:=1+2\n}\n"
	bad := "A( {\n"
	if err := os.WriteFile(filepath.Join(dir, "a.mk"), []byte(ugly), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.mk"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("fmt", "--check", dir)
	if st != 1 || !strings.Contains(out, "a.mk") || strings.Contains(out, "b.mk") || !strings.Contains(errOut, "b.mk: syntax error") {
		t.Fatalf("--check: status %d stdout %q stderr %q", st, out, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.mk")); string(got) != ugly {
		t.Fatalf("--check wrote the file: %q", got)
	}
	if st, _, _ := mk("fmt", dir); st != 1 {
		t.Fatalf("fmt with a syntax error: status %d", st)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.mk")); string(got) != "A() {\n    x := 1 + 2\n}\n" {
		t.Fatalf("fmt wrote %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "b.mk")); string(got) != bad {
		t.Fatalf("fmt rewrote a file that does not parse: %q", got)
	}
	os.Remove(filepath.Join(dir, "b.mk"))
	if st, out, errOut := mk("fmt", "-l", dir); st != 0 || out != "" {
		t.Fatalf("second check: status %d stdout %q stderr %q", st, out, errOut)
	}
	if st, _, _ := mk("fmt", filepath.Join(dir, "missing.mk")); st != 2 {
		t.Errorf("missing path: status %d", st)
	}
}

// TestTestSimAndPropFlags: --sim takes an unsigned decimal (else usage, exit
// 2); --prop runs the property tester.
func TestTestSimAndPropFlags(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "t.mk")
	if err := os.WriteFile(f, []byte("test \"x\" {\n    Assert(true)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _, errOut := mk("test", "--sim", "-1", f); st != 2 || !strings.Contains(errOut, "--sim") {
		t.Errorf("--sim -1: status %d stderr %q", st, errOut)
	}
	// --prop runs the property tester (no contract here: an empty report, exit 0).
	if st, out, errOut := mk("test", "--prop", f); st != 0 || !strings.Contains(out, "0 passed, 0 failed (prop seed ") {
		t.Errorf("--prop: status %d stdout %q stderr %q", st, out, errOut)
	}
}

// TestExplainCommand: a code answers in text and JSON; an unknown code or
// none is a usage error.
func TestExplainCommand(t *testing.T) {
	st, out, _ := mk("explain", "E0501")
	if st != 0 || !strings.HasPrefix(out, "E0501 exclusive-conflict (flow)\n") || !strings.Contains(out, "rejected (") {
		t.Fatalf("status %d out %q", st, out)
	}
	if st, out, _ := mk("explain", "--json", "e0208"); st != 0 || !strings.HasPrefix(out, `{"code":"E0208","name":"unused"`) {
		t.Errorf("json: status %d out %q", st, out)
	}
	for _, args := range [][]string{{"explain"}, {"explain", "E9999"}, {"explain", "E0501", "E0502"}} {
		if st, _, _ := mk(args...); st != 2 {
			t.Errorf("%q: status %d", args, st)
		}
	}
}

// TestFixCommand: mk fix prints the chain and the files it wrote, exits 1
// with the compiles fix left on stderr.
func TestFixCommand(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "driver", "testdata", "fix", "habits.mk"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "habits.mk")
	if err := os.WriteFile(f, src, 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("fix", f)
	// Main's set lacks OutOfMemory, so the try fix does not compile.
	if st != 1 || !strings.Contains(out, "round 1 (parse, 2 diagnostics):") || !strings.Contains(out, "round 2 (types") ||
		!strings.Contains(out, "wrote ") || !strings.Contains(errOut, "E0802") || !strings.Contains(errOut, "pass the error to the caller (not checked to compile") {
		t.Fatalf("status %d\nstdout %s\nstderr %s", st, out, errOut)
	}
}

// TestFixCommandJSON: under --json stdout is one JSON object holding
// the chain and the final report (itself the mk check --json object,
// version first), with no text line before or after it.
func TestFixCommandJSON(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "driver", "testdata", "fix", "habits.mk"))
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "habits.mk")
	if err := os.WriteFile(f, src, 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, errOut := mk("fix", "--dry-run", "--json", f)
	var doc struct {
		Version int `json:"version"`
		Rounds  []struct {
			Phase       string `json:"phase"`
			Diagnostics int    `json:"diagnostics"`
			Applied     []struct {
				Code string `json:"code"`
			} `json:"applied"`
		} `json:"rounds"`
		Files   []string `json:"files"`
		Written bool     `json:"written"`
		Stopped string   `json:"stopped"`
		Report  struct {
			Version     int               `json:"version"`
			Diagnostics []json.RawMessage `json:"diagnostics"`
		} `json:"report"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstatus %d\nstdout %s\nstderr %s", err, st, out, errOut)
	}
	if dec.More() || !strings.HasSuffix(out, "}\n") {
		t.Fatalf("stdout holds more than one object:\n%s", out)
	}
	if st != 1 || doc.Version != 1 || len(doc.Rounds) < 2 || doc.Rounds[0].Phase != "parse" || doc.Rounds[0].Diagnostics != 2 ||
		len(doc.Files) != 1 || doc.Written || doc.Stopped == "" || doc.Report.Version != 1 || len(doc.Report.Diagnostics) == 0 ||
		!strings.Contains(out, `"E0802"`) {
		t.Errorf("status %d doc %+v\nstdout %s", st, doc, out)
	}
	if got, _ := os.ReadFile(f); string(got) != string(src) {
		t.Error("--dry-run --json wrote the file")
	}
}

// TestAPIDiffCommand: two roots give one line per change and exit 1 on a
// breaking one; one root is a usage error.
func TestAPIDiffCommand(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(a, "p.mk"), []byte("export F(a: int): int {\n    return a\n}\n"), 0o644)
	os.WriteFile(filepath.Join(b, "p.mk"), []byte("export F(a: i64): i64 {\n    return a\n}\n"), 0o644)
	st, out, errOut := mk("apidiff", filepath.Join(a, "p.mk"), filepath.Join(b, "p.mk"))
	if st != 1 || !strings.Contains(out, "breaking  main.F  signature") {
		t.Fatalf("status %d out %q err %q", st, out, errOut)
	}
	if st, out, _ := mk("apidiff", "--json", filepath.Join(a, "p.mk"), filepath.Join(a, "p.mk")); st != 0 || out != "{\"changes\":[]}\n" {
		t.Errorf("same: status %d out %q", st, out)
	}
	if st, _, _ := mk("apidiff", a); st != 2 {
		t.Errorf("one root: status %d", st)
	}
}

// TestQueryCommand: an answer exits 0, an unknown name 1, an unknown query 2.
func TestQueryCommand(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "q.mk")
	os.WriteFile(f, []byte("export struct P {\n    export x: int\n}\n"), 0o644)
	if st, out, errOut := mk("query", "members", "P", "--root", f); st != 0 || out != "export x: int\n" {
		t.Errorf("members: status %d out %q err %q", st, out, errOut)
	}
	if st, out, _ := mk("query", "sig", "Nope", "--root", f, "--json"); st != 1 || !strings.Contains(out, `"found":false`) {
		t.Errorf("not found: status %d out %q", st, out)
	}
	if st, _, _ := mk("query", "frobnicate", "--root", f); st != 2 {
		t.Errorf("unknown query: status %d", st)
	}
}

// TestBuildBenchFlags: --object (repeatable) fills Config.ExtraObjects in
// order; the hidden --no-bounds-checks and --no-overflow-checks set the two
// TestHooks fields and are left out of -h.
func TestBuildBenchFlags(t *testing.T) {
	var nf nativeFlags
	var errb strings.Builder
	fs := newNativeFlagSet("build", &errb, &nf)
	if _, _, err := parseArgs(fs, []string{"--object", "a.obj", "--object=b.obj", "--no-bounds-checks", "--no-overflow-checks", "x.mk"}, false); err != nil {
		t.Fatal(err)
	}
	cfg, ok := nf.config("x.mk", &errb)
	if !ok {
		t.Fatalf("config: %s", errb.String())
	}
	if want := []string{"a.obj", "b.obj"}; !slices.Equal(cfg.ExtraObjects, want) {
		t.Errorf("ExtraObjects %q, want %q", cfg.ExtraObjects, want)
	}
	if !cfg.TestHooks.NoBoundsChecks || !cfg.TestHooks.NoOverflowChecks {
		t.Errorf("hooks %+v", cfg.TestHooks)
	}
	var plain nativeFlags
	fs = newNativeFlagSet("build", &errb, &plain)
	if _, _, err := parseArgs(fs, []string{"x.mk"}, false); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := plain.config("x.mk", &errb); cfg.ExtraObjects != nil || cfg.TestHooks.NoBoundsChecks || cfg.TestHooks.NoOverflowChecks {
		t.Errorf("a plain build sets %q %+v", cfg.ExtraObjects, cfg.TestHooks)
	}
	st, _, help := mk("build", "-h")
	if st != 2 || !strings.Contains(help, "-object") || strings.Contains(help, "no-bounds-checks") || strings.Contains(help, "no-overflow-checks") {
		t.Errorf("-h: status %d\n%s", st, help)
	}
	if st, _, _ := mk("run", "--no-bounds-checks", "x.mk"); st != 2 {
		t.Errorf("mk run --no-bounds-checks: status %d, want a usage error (build only)", st)
	}
}

// TestBuildEmitLLFlag: the hidden --emit-ll <dir> sets TestHooks.LLDir, is
// left out of -h, and is mk build's only.
func TestBuildEmitLLFlag(t *testing.T) {
	var nf nativeFlags
	var errb strings.Builder
	fs := newNativeFlagSet("build", &errb, &nf)
	if _, _, err := parseArgs(fs, []string{"--emit-ll", "irdir", "x.mk"}, false); err != nil {
		t.Fatal(err)
	}
	cfg, ok := nf.config("x.mk", &errb)
	if !ok || cfg.TestHooks.LLDir != "irdir" {
		t.Errorf("LLDir %q (ok %v), want irdir: %s", cfg.TestHooks.LLDir, ok, errb.String())
	}
	var plain nativeFlags
	fs = newNativeFlagSet("build", &errb, &plain)
	if _, _, err := parseArgs(fs, []string{"x.mk"}, false); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := plain.config("x.mk", &errb); cfg.TestHooks.LLDir != "" {
		t.Errorf("a plain build sets LLDir %q", cfg.TestHooks.LLDir)
	}
	if st, _, help := mk("build", "-h"); st != 2 || strings.Contains(help, "emit-ll") {
		t.Errorf("-h: status %d\n%s", st, help)
	}
	if st, _, _ := mk("run", "--emit-ll", "d", "x.mk"); st != 2 {
		t.Errorf("mk run --emit-ll: status %d, want a usage error (build only)", st)
	}
	// An empty directory is a usage error, not a build without the dump.
	var empty nativeFlags
	fs = newNativeFlagSet("build", &errb, &empty)
	if _, _, err := parseArgs(fs, []string{"--emit-ll=", "x.mk"}, false); err == nil {
		t.Error("--emit-ll= parsed without an error")
	}
	if st, _, msg := mk("build", "--emit-ll=", "x.mk"); st != 2 || !strings.Contains(msg, "empty directory") {
		t.Errorf("mk build --emit-ll=: status %d, want a usage error\n%s", st, msg)
	}
}
