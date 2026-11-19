// Why: names-group blocks should be judged even with a std that cannot carry
// a block through types, so their verdicts do not wait for the whole std.
// What: the stand-in std builders (standInStd, standInStdNames, noCoreStd),
// TestCorpusThroughNames and TestConformanceThroughNames.
// How: a std whose core declares every prelude name but no bodies, with one
// planted types error, so the driver judges units through names only.
//
// Every reject block of the parse and names groups is PASS or PENDING std:
// with a stand-in std whose std.core declares every prelude name of the
// reference, the driver judges units through names (the driver's Through),
// so names-group blocks are judged, not held pending.

package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

// preludeNamesFromReference reads the reference's prelude line.
func preludeNamesFromReference(t testing.TB, root string) []string {
	t.Helper()
	text, err := readDoc(root, "doc/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range splitLines(text) {
		if rest, ok := strings.CutPrefix(l, "Prelude, no import needed: `"); ok {
			list, _, _ := strings.Cut(rest, "`")
			return strings.Fields(list)
		}
	}
	t.Fatal("no prelude line in doc/reference.md")
	return nil
}

// plantedStdError is a types-group error (E0301, check's) planted in a
// stand-in std so that the driver judges units through names only (the
// driver's Through, rule 2): the stand-in declares the prelude names but
// almost no members, so judging it through types draws E0203 for every member
// a block uses, which says nothing about the block.
const plantedStdError = "export Planted(): int {\n    return \"planted\"\n}\n"

// standInStd writes a std tree whose core declares every prelude name, with
// no body anywhere, and returns its directory. The driver's Through asks for
// every prelude name, Path included (std/core/core_path.mk).
func standInStd(t testing.TB, root string) string {
	t.Helper()
	return writeStandInStd(t, root, false)
}

// standInStdNames is standInStd with plantedStdError in std.core/planted.mk,
// so units are judged through names.
func standInStdNames(t testing.TB, root string) string {
	t.Helper()
	return writeStandInStd(t, root, true)
}

func writeStandInStd(t testing.TB, root string, planted bool) string {
	t.Helper()
	generic := map[string]string{"Box": "[T]", "Cell": "[T]", "Chan": "[T]", "Guard": "[T]", "HeapList": "[T]",
		"HeapMap": "[K, V]", "List": "[T]", "Map": "[K, V]", "MapIter": "[K, V]", "Mutex": "[T]", "Set": "[T]",
		"Scope": "[E]", "TextMap": "[V]", "TextMapIter": "[V]", "Slice": "[T]"}
	var b strings.Builder
	for _, n := range preludeNamesFromReference(t, root) {
		switch n {
		case "Path": // a view struct in std.core, no longer reserved
			b.WriteString("export view struct Path {}\n")
		case "Canceled", "Closed", "OutOfMemory", "ParseError", "ReadError", "Utf8Error", "WriteError":
			b.WriteString("export error " + n + "\n")
		case "Eq", "Ord", "Hash", "Slice": // Writer and Reader are view structs
			b.WriteString("export interface " + n + generic[n] + " {}\n")
		case "Format":
			b.WriteString(standInFormat)
		case "Print":
			b.WriteString(standInPrint)
		case "Arch", "Os", "StackSize":
			b.WriteString("export enum " + n + " { A }\n")
		default:
			b.WriteString("export struct " + n + generic[n] + " {}\n")
		}
	}
	files := stdManifestFiles()
	files["core/core.mk"] = b.String()
	files["mmio/mmio.mk"] = stdFileText("mmio/mmio.mk")
	if planted {
		files["core/planted.mk"] = plantedStdError
	}
	return writeStdTree(t, files)
}

// standInFormat and standInPrint are the members the stand-in std does
// declare: the printing surface of std.core as std/core/core_iface.mk,
// core_fmt.mk, core_rw.mk and io_system.mk declare it, bodiless. The checks
// of Print (its shape, its record and format text, E0705 and E0706) are
// types-stage rules over exactly these signatures, so the reject blocks
// that test them are judged through types and need them.
var standInFormat = "export interface Format {\n    Format(&self, w: mut Writer): WriteError!void\n}\n" + standInFormatImpls()

// standInFormatImpls are core_impls.mk's built-in Format impls of the
// primitive types, each with an empty body (an impl method needs one, E0101).
func standInFormatImpls() string {
	var b strings.Builder
	for _, t := range []string{"i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "int", "uint", "f32", "f64", "bool", "char", "str"} {
		b.WriteString("impl Format for " + t + " {\n    Format(&self, w: mut Writer): WriteError!void {}\n}\n")
	}
	return b.String()
}

const standInPrint = "export Print[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void\n" +
	"export Writer.Str(mut self, s: str): WriteError!void\n" +
	"export System.Print[A: Fields[Format] + Plain](&self, const f: str, args: A): WriteError!void\n" +
	"export System.PrintErr[A: Fields[Format] + Plain](&self, const f: str, args: A): WriteError!void\n"

// stdManifestFiles are the two files every std tree needs before a module
// is loaded: std's manifest pkg.mk and std.pkg, which declares pkg.Package.
func stdManifestFiles() map[string]string {
	return map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"std\",\n    bins: [],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
		"pkg/pkg.mk": stdPkgText(),
	}
}

// stdPkgText is the real std.pkg (std/pkg/pkg.mk). The manifest is
// evaluated, so a stand-in std needs the structs the manifest step decodes.
func stdPkgText() string { return stdFileText("pkg/pkg.mk") }

// stdFileText is the real std file at p (std-relative, '/'), LF-normalized.
// The stand-in takes std.mmio whole: every function of it is bodiless, an
// intrinsic, and the reference's mmio blocks import it (E1101 on each call).
func stdFileText(p string) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "std", filepath.FromSlash(p)))
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// noCoreStd is a std with a manifest and std.pkg but no std.core, so
// std declares no prelude name.
func noCoreStd(t testing.TB) string {
	t.Helper()
	return writeStdTree(t, stdManifestFiles())
}

// writeStdTree writes files (std-relative paths with '/') into a new
// temporary directory and returns it.
func writeStdTree(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCorpusThroughNames: with the stand-in std and its planted types
// error, Through is names, so every reject block of the parse and names
// groups is PASS or PENDING std:, and every block that needs
// types is PENDING std: by rule 6, never FAIL.
func TestCorpusThroughNames(t *testing.T) {
	root := repoRoot(t)
	r, err := Run(Options{Driver: driver.Config{StdDir: standInStdNames(t, root)}})
	if err != nil {
		t.Fatal(err)
	}
	checkPlanted(t, r)
	seen := map[int]int{}
	early := 0
	for _, u := range r.Units {
		if checkConflict(t, u, r.Std.Through, seen) {
			continue
		}
		if checkE0101(u.Unit, r.Std.Through) {
			early++
			t.Logf("%s %s %s: its E0101 is check's, judged through types", u.Unit.Key, verdictText(u.Unit), u.Status)
			continue
		}
		need, group := unitNeed(u.Unit)
		within := need&driver.Available() == need && group <= r.Std.Through
		switch {
		case u.Status == StatusFail:
			t.Errorf("%s %s FAIL: %s", u.Unit.Key, verdictText(u.Unit), u.Reason)
		case within && u.Unit.Verdict == VerdictReject && u.Status == StatusPending && !strings.HasPrefix(u.Reason, "std: "):
			t.Errorf("%s through resolve is PENDING %s; want PASS or PENDING std:", u.Unit.Key, u.Reason)
		}
		if within && u.Unit.Verdict == VerdictReject {
			t.Logf("through resolve:%s %s %s %s", u.Unit.Key, verdictText(u.Unit), u.Status, u.Reason)
		}
	}
	checkConflictsSeen(t, r.Std.Through, seen)
	if early != len(checkE0101Lines) {
		t.Errorf("checkE0101Lines matched %d blocks, want %d", early, len(checkE0101Lines))
	}
	s := r.Summary
	t.Logf("through %s: pass %d, fail %d, pending %d; missing %d", r.Std.Through, s.Pass, s.Fail, s.Pending, len(r.Std.State.Missing))
}

// checkPlanted: a run on standInStdNames is judged through names, and std's
// report is exactly the planted E0301 in std/core/planted.mk.
func checkPlanted(t *testing.T, r *Result) {
	t.Helper()
	ds := r.Std.State.Report.Diags
	if r.Std.Through != diag.PhaseNames || len(ds) != 1 || ds[0].Diag.Code != diag.E0301 || !strings.Contains(r.Std.Text, "planted.mk") {
		var b strings.Builder
		_ = WriteText(&b, r)
		t.Fatalf("stand-in std with the planted error: through %s, %d std diagnostics, want names and the one planted E0301\n%s", r.Std.Through, len(ds), b.String())
	}
}

// TestConformanceThroughNames: the conformance units against the stand-in
// std, judged through names. It logs, never fails: the stand-in std lacks
// the names units take from std modules and from the generator API (Shared,
// AtomicInt, Gen, Type), which StdState.Missing cannot name yet, so those
// FAILs say nothing about the units.
func TestConformanceThroughNames(t *testing.T) {
	root := repoRoot(t)
	r, err := Run(Options{Conformance: true, Only: "tests/conformance/*/*", Driver: driver.Config{StdDir: standInStdNames(t, root)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range r.Units {
		if _, ok := knownUnitFailures[u.Unit.Key]; ok {
			continue
		}
		if u.Status == StatusFail {
			t.Logf("%s %s FAIL: %s", u.Unit.Key, verdictText(u.Unit), u.Reason)
		}
	}
	s := r.Summary
	t.Logf("through %s: %d units, pass %d, fail %d, pending %d", r.Std.Through, s.Units, s.Pass, s.Fail, s.Pending)
}
