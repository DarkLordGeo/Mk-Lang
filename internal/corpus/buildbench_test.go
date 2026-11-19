// Why: the compiler has compile-speed budgets (clean build, check speed, warm
// edits), and they need a harness that measures real builds of large programs.
// What: BenchmarkCleanBuild100K, BenchmarkCheckScaling, the BenchmarkWarmCheck*
// and BenchmarkWarmBuild* benchmarks, over programs from Generate.
// How: each benchmark builds or checks through driver's exported API with std
// cached, and reports per-iteration counts beside ns/op.
//
// The compile-speed benchmarks: BenchmarkCleanBuild100K, BenchmarkCheckScaling,
// BenchmarkWarmCheckBodyEdit1M, BenchmarkWarmCheckSigEdit100K,
// BenchmarkWarmBuildOneFunction, BenchmarkWarmBuildGenericEdit,
// BenchmarkWarmCheckNoEdit, plus BenchmarkWarmBuildNoChange, the warm build
// with nothing changed that the link reuse acts on.
//
// They live here, not in internal/driver, because their programs are
// Generate's and corpus imports driver: a driver test cannot import corpus
// (archtest's TestTestEdgesAreSafe refuses that edge). They use only
// driver's exported API, as mk does.
//
// Each benchmark reports, beside ns/op, what the builds did per iteration:
// clang packs compiled, packs whose IR codegen generated (emitted, the
// support pack included), packs taken from the cache (hits), executables
// linked, `clang --version` processes, files parsed, modules resolved and
// functions checked, and the wall time of the parse and resolve stages
// (WarmCheckBodyEdit1M's "reports parse and resolve time separately"). A
// timing from a loaded machine is not a claim: these run by name on an idle
// machine, and other runs use only -benchtime=1x to see that they work.
// Toolchain discovery is left to the driver (Config.Tools nil), so its
// process is in every build, as in mk build.
//
// MK_BUILDBENCH_DIV=<n> divides every size by n, for a smoke run; the
// numbers of such a run are not at the budgets' sizes.

package corpus

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/driver"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// benchKLOC is n thousand lines, divided by MK_BUILDBENCH_DIV when set.
func benchKLOC(n int) int {
	if d, err := strconv.Atoi(os.Getenv("MK_BUILDBENCH_DIV")); err == nil && d > 1 {
		return max(1, n/d)
	}
	return n
}

// benchFiles is Generate(seed, kloc) by path.
func benchFiles(seed int64, kloc int) map[string]string {
	files := map[string]string{}
	for _, f := range Generate(seed, kloc) {
		files[f.Path] = f.Text
	}
	return files
}

// benchBin makes Generate's library a package with one bin, gen, rooted at
// main.mk, whose Main calls Total (and uses.Use when the package has the
// generic module, benchGeneric).
func benchBin(b testing.TB, files map[string]string) {
	b.Helper()
	m := files["pkg.mk"]
	if !strings.Contains(m, "    bins: [],\n") {
		b.Fatal("Generate's manifest has no empty bins list")
	}
	files["pkg.mk"] = strings.Replace(m, "    bins: [],\n",
		"    bins: [pkg.Bin(name: \"gen\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n", 1)
	expr := "Total(3)"
	if _, ok := files["uses/uses.mk"]; ok {
		files["main.mk"] = "import uses\n" + files["main.mk"]
		expr = "Total(3) + uses.Use(3)"
	}
	files["main.mk"] += "\nexport Main(sys: &System): int {\n    return " + expr + " % 7\n}\n"
}

// genericInstances is BenchmarkWarmBuildGenericEdit's instance count (the
// budget: "body edit in a generic at most 500 ms for 100 live instances").
const genericInstances = 100

// genericBody is pick.Pass's text with the modulus k: the edit changes k.
// Both exits return x, so x is consumed on each (E0402).
func genericBody(k int) string {
	return fmt.Sprintf("export Pass[X](x: X, n: int): X {\n    if n %% %d == 0 {\n        return x\n    }\n    return x\n}\n", k)
}

// benchGeneric adds the module pick, one generic function, and the module
// uses, which instantiates it at genericInstances struct types.
func benchGeneric(files map[string]string) {
	files["pick/pick.mk"] = genericBody(7)
	var w strings.Builder
	w.WriteString("import pick\n")
	for i := 1; i <= genericInstances; i++ {
		fmt.Fprintf(&w, "\nexport struct T%03d {\n    export v: int\n}\n", i)
	}
	w.WriteString("\nexport Use(n: int): int {\n    var t := 0\n")
	for i := 1; i <= genericInstances; i++ {
		fmt.Fprintf(&w, "    t += pick.Pass(T%03d(v: n + %d), n).v\n", i, i)
	}
	w.WriteString("    return t\n}\n")
	files["uses/uses.mk"] = w.String()
}

// editTarget is the file holding the Entry of the middle part module of a
// Generate(seed, kloc) package: the place of every edit below.
func editTarget(b testing.TB, files map[string]string, kloc int) string {
	b.Helper()
	part := fmt.Sprintf("part%02d/", (max(1, kloc/genModuleKLOC)+1)/2)
	for p, text := range files {
		if strings.HasPrefix(p, part) && strings.Contains(text, entryHead) {
			return p
		}
	}
	b.Fatalf("no file of %s holds Entry", part)
	return ""
}

// entryHead is the start of every part's Entry (generator.entry).
const entryHead = "export Entry(n: int): int {\n    var t := 0\n"

// bodyEdit is Entry with a new initial value v: a body edit.
func bodyEdit(text string, v int) string {
	return strings.Replace(text, entryHead, fmt.Sprintf("export Entry(n: int): int {\n    var t := %d\n", v), 1)
}

// sigEdit is Entry with its parameter renamed to n<v>: a signature edit
// (main, which calls Entry, depends on it).
func sigEdit(text string, v int) string {
	return strings.Replace(text, entryHead, fmt.Sprintf("export Entry(n%d: int): int {\n    n := n%d\n    var t := 0\n", v, v), 1)
}

// writeFiles writes files under dir.
func writeFiles(b testing.TB, dir string, files map[string]string) {
	b.Helper()
	for p, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

// copyTree copies the regular files under src to dst.
func copyTree(b testing.TB, src, dst string) {
	b.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		b.Fatal(err)
	}
}

// benchCounts are what a benchmark's builds did, summed over iterations.
type benchCounts struct {
	mu       sync.Mutex
	links    int
	versions int
	compiled int
	emitted  int
	hits     int
	parsed   int
	resolved int
	checked  int
	parseNS  int64
	resolNS  int64
}

// note adds one build's trace and stage times.
func (c *benchCounts) note(r driver.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tr := r.Trace; tr != nil {
		c.compiled += len(tr.PacksCompiled)
		c.emitted += len(tr.PacksEmitted)
		c.hits += len(tr.PacksFromCache)
		c.parsed += len(tr.FilesParsed)
		c.resolved += len(tr.ModulesResolved)
		c.checked += len(tr.ItemsChecked)
	}
	for _, st := range r.Timings {
		switch st.Stage {
		case "parse":
			c.parseNS += st.Took.Nanoseconds()
		case "resolve":
			c.resolNS += st.Took.Nanoseconds()
		}
	}
}

// report writes the per-iteration metrics.
func (c *benchCounts) report(b *testing.B) {
	n := float64(b.N)
	b.ReportMetric(float64(c.compiled)/n, "packs/op")
	b.ReportMetric(float64(c.emitted)/n, "emitted/op")
	b.ReportMetric(float64(c.hits)/n, "hits/op")
	b.ReportMetric(float64(c.links)/n, "links/op")
	b.ReportMetric(float64(c.versions)/n, "versions/op")
	b.ReportMetric(float64(c.parsed)/n, "parsed/op")
	b.ReportMetric(float64(c.resolved)/n, "resolved/op")
	b.ReportMetric(float64(c.checked)/n, "checked/op")
	b.ReportMetric(float64(c.parseNS)/n, "parse-ns/op")
	b.ReportMetric(float64(c.resolNS)/n, "resolve-ns/op")
}

// benchCfg is the Config of one build or check of root, counting into c.
func benchCfg(root, cache string, target types.Target, prof types.Profile, jobs int, c *benchCounts) driver.Config {
	return driver.Config{Root: root, CacheDir: cache, Target: target, Profile: prof, Jobs: jobs, Timings: io.Discard,
		TestHooks: driver.TestHooks{
			OnLink:       func(toolchain.LinkJob) { c.mu.Lock(); c.links++; c.mu.Unlock() },
			OnVersionRun: func(string) { c.mu.Lock(); c.versions++; c.mu.Unlock() },
		}}
}

// benchBuild is one mk build of cfg into out, in a new session as each mk
// process is; a diagnostic is fatal.
func benchBuild(b testing.TB, cfg driver.Config, out string, c *benchCounts) driver.Result {
	b.Helper()
	s, err := driver.NewSession(cfg)
	if err != nil {
		b.Fatal(err)
	}
	var r driver.Result
	var paths []string
	measured(c, func() { r, paths, err = s.Build(out) })
	if err != nil {
		b.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		b.Fatalf("build of %s: %d paths\n%s", cfg.Root, len(paths), r.Report.Text(r.Sources))
	}
	if c != nil {
		c.note(r)
	}
	return r
}

// benchCheck is one mk check of cfg in a new session; a diagnostic is fatal.
func benchCheck(b testing.TB, cfg driver.Config, c *benchCounts) driver.Result {
	b.Helper()
	s, err := driver.NewSession(cfg)
	if err != nil {
		b.Fatal(err)
	}
	var r driver.Result
	measured(c, func() { r = s.Check() })
	if len(r.Report.Diags) > 0 {
		b.Fatalf("check of %s:\n%s", cfg.Root, r.Report.Text(r.Sources))
	}
	if c != nil {
		c.note(r)
	}
	return r
}

// measured runs f, a measured build or check (one with counts c), under
// the pprof label mkbench=measured, which the goroutines it starts
// inherit. b.ResetTimer does not reset a -test.cpuprofile, so without the
// label the setup's cold build dominates the profile; `go tool pprof
// -tagfocus mkbench=measured` keeps only the measured builds. The setup's
// builds (c nil) run unlabelled.
func measured(c *benchCounts, f func()) {
	if c == nil {
		f()
		return
	}
	pprof.Do(context.Background(), pprof.Labels("mkbench", "measured"), func(context.Context) { f() })
}

// stdCache returns a cache under b's temporary directory that already
// holds std for target and prof: the front end's entries and, with build,
// the objects and link of a one-function program. "Clean" below means
// user code cold and std warm ("std cached"); each iteration copies it.
func stdCache(b *testing.B, target types.Target, prof types.Profile, build bool) string {
	b.Helper()
	dir := b.TempDir()
	src := filepath.Join(dir, "src")
	writeFiles(b, src, map[string]string{"main.mk": "export Main(sys: &System): int {\n    return 0\n}\n"})
	cache := filepath.Join(dir, "cache")
	cfg := driver.Config{Root: filepath.Join(src, "main.mk"), CacheDir: cache, Target: target, Profile: prof}
	if build {
		benchBuild(b, cfg, filepath.Join(dir, "out", "main"+exeSuffixOf(target)), nil)
	} else {
		benchCheck(b, cfg, nil)
	}
	return cache
}

// exeSuffixOf is ".exe" for a Windows target.
func exeSuffixOf(t types.Target) string {
	if t.OS == types.OSWindows {
		return ".exe"
	}
	return ""
}

// benchLines is the number of lines in files.
func benchLines(files map[string]string) int {
	n := 0
	for _, text := range files {
		n += strings.Count(text, "\n")
	}
	return n
}

// BenchmarkCleanBuild100K (budget "clean dev build of
// user code at most 2 s at 100 KLOC", and "release clean at most 5
// minutes" as release): mk build of Generate(1, 100) with a bin, its user
// code cold and std in the cache (a copy of stdCache per iteration).
func BenchmarkCleanBuild100K(b *testing.B) {
	target := types.X86_64Windows
	files := benchFiles(1, benchKLOC(100))
	benchBin(b, files)
	src := b.TempDir()
	writeFiles(b, src, files)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		tmpl := stdCache(b, target, prof, true)
		b.Run(prof.String(), func(b *testing.B) {
			var c benchCounts
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache := filepath.Join(b.TempDir(), "cache")
				copyTree(b, tmpl, cache)
				cfg := benchCfg(src, cache, target, prof, 0, &c)
				out := filepath.Join(b.TempDir(), "gen.exe")
				b.StartTimer()
				benchBuild(b, cfg, out, &c)
			}
			b.StopTimer()
			c.report(b)
			b.ReportMetric(float64(benchLines(files)), "lines/op")
		})
	}
}

// chainPackage is CheckScaling's package ("two independent import chains,
// one long and one of heavy modules"): base, a chain of longN modules of
// about 1 KLOC (l01 imports base, l02 imports l01, ...), a chain of heavyN
// modules of about 7 KLOC (h1 ... h4), and main, which imports every
// module. Each module is the one part of a Generate package of its size,
// renamed, over the shared base (every Generate base exports the same
// names); link.mk makes the chain edge a use, not only an import.
func chainPackage(longN, heavyN, heavyKLOC int) map[string]string {
	files := map[string]string{}
	all := benchFiles(1, 1)
	files["base/base.mk"], files["pkg.mk"] = all["base/base.mk"], all["pkg.mk"]
	var mods []string
	chain := func(prefix string, n, kloc int, seed int64) {
		prev := ""
		for i := 1; i <= n; i++ {
			mod := fmt.Sprintf("%s%02d", prefix, i)
			for p, text := range benchFiles(seed+int64(i), kloc) {
				if rest, ok := strings.CutPrefix(p, "part01/"); ok {
					files[mod+"/"+rest] = text
				}
			}
			if prev != "" {
				files[mod+"/link.mk"] = fmt.Sprintf("import %s\n\nexport Link(n: int): int {\n    return %s.Entry(n) + 1\n}\n", prev, prev)
			}
			mods = append(mods, mod)
			prev = mod
		}
	}
	chain("l", longN, 1, 100)
	chain("h", heavyN, heavyKLOC, 200)
	var w strings.Builder
	w.WriteString("import base\n")
	for _, m := range mods {
		fmt.Fprintf(&w, "import %s\n", m)
	}
	w.WriteString("\nexport Total(n: int): int {\n    var t := base.Mix(n, 1)\n")
	for _, m := range mods {
		fmt.Fprintf(&w, "    t += %s.Entry(n + t %% 5)\n", m)
	}
	w.WriteString("    return t\n}\n")
	files["main.mk"] = w.String()
	return files
}

// BenchmarkCheckScaling (budget "check at least 150
// KLOC/s per core, at least 5x on 8 cores"): mk check of chainPackage at
// -j 1 and -j 8, user code cold and std in the cache. lines/op over the
// time per op is the check speed; the two sub-benchmarks' ratio is the
// scaling.
func BenchmarkCheckScaling(b *testing.B) {
	files := chainPackage(24, 4, max(1, benchKLOC(7)))
	src := b.TempDir()
	writeFiles(b, src, files)
	tmpl := stdCache(b, types.X86_64Windows, types.Dev, false)
	for _, jobs := range []int{1, 8} {
		b.Run(fmt.Sprintf("j%d", jobs), func(b *testing.B) {
			var c benchCounts
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache := filepath.Join(b.TempDir(), "cache")
				copyTree(b, tmpl, cache)
				cfg := benchCfg(src, cache, types.X86_64Windows, types.Dev, jobs, &c)
				b.StartTimer()
				benchCheck(b, cfg, &c)
			}
			b.StopTimer()
			c.report(b)
			b.ReportMetric(float64(benchLines(files)), "lines/op")
		})
	}
}

// warmCheckEdit is a warm mk check after an edit per iteration: Generate(1,
// kloc) checked once into a fresh cache, then edit(text, i) applied to the
// middle part's Entry file and the package checked again.
func warmCheckEdit(b *testing.B, kloc int, edit func(string, int) string) {
	files := benchFiles(1, kloc)
	src := b.TempDir()
	writeFiles(b, src, files)
	cache := filepath.Join(b.TempDir(), "cache")
	benchCheck(b, benchCfg(src, cache, types.X86_64Windows, types.Dev, 0, &benchCounts{}), nil)
	var target string
	if edit != nil {
		target = editTarget(b, files, kloc)
	}
	var c benchCounts
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if edit != nil {
			b.StopTimer()
			text := edit(files[target], i+1)
			if text == files[target] {
				b.Fatal("the edit changed nothing")
			}
			writeFiles(b, src, map[string]string{target: text})
			b.StartTimer()
		}
		benchCheck(b, benchCfg(src, cache, types.X86_64Windows, types.Dev, 0, &c), &c)
	}
	b.StopTimer()
	c.report(b)
	b.ReportMetric(float64(benchLines(files)), "lines/op")
}

// BenchmarkWarmCheckBodyEdit1M (budget "edit to diagnostic: body edit at
// most 100 ms at 1 MLOC"): a warm mk check of Generate(1, 1000) after one
// body edit, with parse and resolve reported. Run it alone: it needs several
// GB of memory (4.7 GB peak at 1 MLOC in one run of this benchmark).
func BenchmarkWarmCheckBodyEdit1M(b *testing.B) {
	warmCheckEdit(b, benchKLOC(1000), bodyEdit)
}

// BenchmarkWarmCheckSigEdit100K (budget "signature
// edit at most 300 ms at 100 KLOC"): a warm mk check of Generate(1, 100)
// after renaming the parameter of one part's exported Entry, which main
// calls.
func BenchmarkWarmCheckSigEdit100K(b *testing.B) {
	warmCheckEdit(b, benchKLOC(100), sigEdit)
}

// BenchmarkWarmCheckNoEdit ("mk check on a 100-module generated program,
// nothing changed"): Generate(1, 400) is base, main and 100 part modules.
func BenchmarkWarmCheckNoEdit(b *testing.B) {
	warmCheckEdit(b, benchKLOC(400), nil)
}

// warmBuild is a warm mk build per iteration: files built once into a
// fresh cache, then edit (when not nil) applied to file and the package
// built again into the same output path, as an edit loop does.
func warmBuild(b *testing.B, target types.Target, prof types.Profile, files map[string]string, file string, edit func(string, int) string) {
	src := b.TempDir()
	writeFiles(b, src, files)
	cache := filepath.Join(b.TempDir(), "cache")
	out := filepath.Join(b.TempDir(), "gen"+exeSuffixOf(target))
	benchBuild(b, benchCfg(src, cache, target, prof, 0, &benchCounts{}), out, nil)
	var c benchCounts
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if edit != nil {
			b.StopTimer()
			text := edit(files[file], i+1)
			if text == files[file] {
				b.Fatal("the edit changed nothing")
			}
			writeFiles(b, src, map[string]string{file: text})
			b.StartTimer()
		}
		r := benchBuild(b, benchCfg(src, cache, target, prof, 0, &c), out, &c)
		if edit != nil && len(r.Trace.PacksCompiled) == 0 {
			b.Fatal("the edit build compiled no pack")
		}
	}
	b.StopTimer()
	c.report(b)
	b.ReportMetric(float64(benchLines(files)), "lines/op")
}

// BenchmarkWarmBuildOneFunction (budget "dev build
// after a one-function edit at most 200 ms ELF, 350 ms COFF at 100
// KLOC"): one body edit of Generate(1, 100) with a bin, then mk build, on
// both targets (the ELF build is a cross build; nothing runs).
func BenchmarkWarmBuildOneFunction(b *testing.B) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		b.Run(target.Name, func(b *testing.B) {
			kloc := benchKLOC(100)
			files := benchFiles(1, kloc)
			benchBin(b, files)
			warmBuild(b, target, types.Dev, files, editTarget(b, files, kloc), bodyEdit)
		})
	}
}

// BenchmarkWarmBuildGenericEdit (budget "generic body
// edit at most 500 ms for 100 instances"): Generate(1, 100) with a bin and
// a generic function instantiated at 100 types, its body edited, then mk
// build.
func BenchmarkWarmBuildGenericEdit(b *testing.B) {
	files := benchFiles(1, benchKLOC(100))
	benchGeneric(files)
	benchBin(b, files)
	warmBuild(b, types.X86_64Windows, types.Dev, files, "pick/pick.mk", func(_ string, i int) string { return genericBody(7 + i) })
}

// BenchmarkWarmBuildNoChange: mk build of an unchanged Generate(1, 100)
// with a bin, dev and release, into the same output path. Not a budget
// row: it is the build that the link reuse and the toolchain version memo
// change, measured from the same harness before and after.
func BenchmarkWarmBuildNoChange(b *testing.B) {
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		b.Run(prof.String(), func(b *testing.B) {
			files := benchFiles(1, benchKLOC(100))
			benchBin(b, files)
			warmBuild(b, types.X86_64Windows, prof, files, "", nil)
		})
	}
}
