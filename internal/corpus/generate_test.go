// Why: the speed benchmarks need large programs that are deterministic and
// check clean.
// What: TestGenerateDeterministic, TestGenerateChecksClean,
// TestGenerateChecksOnDisk and BenchmarkCheck100K.
// How: Generate's output is checked as a corpus unit and as a directory; the
// benchmark reports KLOC/s through b.SetBytes.

package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/driver"
)

// TestGenerateDeterministic: the text is a function of (seed, kloc) only;
// another seed gives another text, the size follows kloc, and the files are
// sorted by path.
func TestGenerateDeterministic(t *testing.T) {
	for _, kloc := range []int{1, 10, 100} {
		a, b := Generate(1, kloc), Generate(1, kloc)
		if !slices.Equal(a, b) {
			t.Fatalf("Generate(1, %d) differs between two calls", kloc)
		}
		if !slices.IsSortedFunc(a, func(x, y UnitFile) int { return strings.Compare(x.Path, y.Path) }) {
			t.Errorf("Generate(1, %d): files not sorted by path", kloc)
		}
		lines := 0
		for _, f := range a {
			lines += strings.Count(f.Text, "\n")
			if !strings.HasSuffix(f.Text, "\n") || strings.Contains(f.Text, "\r") {
				t.Errorf("Generate(1, %d) %s: not LF-terminated LF text", kloc, f.Path)
			}
		}
		// "About kloc thousand lines": within 10%.
		if want := kloc * 1000; lines < want*9/10 || lines > want*11/10 {
			t.Errorf("Generate(1, %d): %d lines, want about %d", kloc, lines, want)
		}
		t.Logf("Generate(1, %d): %d files, %d lines", kloc, len(a), lines)
	}
	if slices.Equal(Generate(1, 10), Generate(2, 10)) {
		t.Error("Generate(1, 10) equals Generate(2, 10)")
	}
}

// TestGenerateChecksClean: the generated package is plain code that the
// compiler judges ok through every stage the session has (no diagnostic, no
// stage skipped, PASS by the one rule set), for three seeds and sizes; and
// the 100 KLOC package spreads over many modules.
func TestGenerateChecksClean(t *testing.T) {
	comp := DriverCompiler(repoRoot(t), driver.Config{})
	info, err := comp.Start()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		seed int64
		kloc int
	}{{1, 1}, {7, 3}, {1, 20}} {
		name := fmt.Sprintf("generate/%d-%d", c.seed, c.kloc)
		u := GenerateUnit(name, c.seed, c.kloc)
		res := comp.Check(u, driver.UnitCheck)
		if len(res.Report.Diags) > 0 {
			t.Errorf("%s: %d diagnostics, want none:\n%s", name, len(res.Report.Diags), res.Report.Text(res.Sources))
		}
		if res.Skipped != 0 {
			t.Errorf("%s: stages skipped: %v", name, res.Skipped.Stages())
		}
		if j := JudgeUnit(u, res, info.State, comp.Available()); j.Status != StatusPass {
			t.Errorf("%s: judged %v (%s), want PASS", name, j.Status, j.Reason)
		}
	}
	// The bodies of every module are really checked: a type error planted
	// in the last file of the last part is reported there.
	u := GenerateUnit("generate/planted", 1, 20)
	last := slices.IndexFunc(u.Files, func(f UnitFile) bool { return f.Path == "pkg.mk" }) - 1
	bad := &u.Files[last]
	if !strings.HasPrefix(bad.Path, "part") {
		t.Fatalf("the file before pkg.mk is %s, not a part's", bad.Path)
	}
	bad.Text += "\nPlanted(): int {\n    return \"no\"\n}\n"
	res := comp.Check(u, driver.UnitCheck)
	if len(res.Report.Diags) == 0 {
		t.Errorf("a type error planted in %s is not reported", bad.Path)
	}
	for _, d := range res.Report.Diags {
		if p := res.Paths[d.Diag.At.File]; p != bad.Path {
			t.Errorf("planted error reported in %q, want %s: %s", p, bad.Path, d.Diag.Message)
		}
	}
	modules := map[string]bool{}
	for _, f := range Generate(1, 100) {
		if i := strings.IndexByte(f.Path, '/'); i > 0 {
			modules[f.Path[:i]] = true
		}
	}
	if len(modules) < 9 {
		t.Errorf("Generate(1, 100) has %d modules besides main, want at least 9 (base and 8 parts)", len(modules))
	}
}

// TestGenerateChecksOnDisk: the build and edit benchmarks run `mk check`
// and `mk build --timings` on Generate's package written to disk, so the
// package must also check clean as a directory through driver.Session, with
// no manifest, not only as a corpus unit.
func TestGenerateChecksOnDisk(t *testing.T) {
	dir := t.TempDir()
	for _, f := range Generate(1, 8) {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.Text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := driver.NewSession(driver.Config{Root: dir, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res := s.Check()
	if len(res.Report.Diags) > 0 {
		t.Errorf("Generate(1, 8) on disk: %d diagnostics, want none:\n%s", len(res.Report.Diags), res.Report.Text(res.Sources))
	}
}

// BenchmarkCheck100K is the check-speed benchmark: Generate(1,
// 100) through CorpusSession.CheckUnit, std checked once before the timer,
// one fresh unit name per iteration (a session refuses a name twice). The
// KLOC/s figure is b.SetBytes' MB/s over the bytes per line (lines/op is
// reported beside it).
func BenchmarkCheck100K(b *testing.B) {
	comp := DriverCompiler(repoRoot(b), driver.Config{})
	if _, err := comp.Start(); err != nil {
		b.Fatal(err)
	}
	tmpl := GenerateUnit("bench/check100k", 1, 100)
	size, lines := 0, 0
	for _, f := range tmpl.Files {
		size += len(f.Text)
		lines += strings.Count(f.Text, "\n")
	}
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		u := *tmpl
		u.Name = fmt.Sprintf("bench/check100k#%d", i)
		res := comp.Check(&u, driver.UnitCheck)
		if len(res.Report.Diags) > 0 {
			b.Fatalf("Generate(1, 100) has diagnostics:\n%s", res.Report.Text(res.Sources))
		}
	}
	b.ReportMetric(float64(lines), "lines/op")
}
