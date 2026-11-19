// Why: coverage numbers are only useful if they are exact, survive a
// killed child, and cover only the package's own files.
// What: tests of mk test --cover (cover.go, std/rt/cover.mk): line counts
// on a hand-counted example, a child the runner kills on its timeout still
// reporting, a kill race, own modules only, refusals and the lcov output.
// How: runs mk test --cover on small packages and compares the per-line
// counts with counts made by hand.

package driver

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// coverSource: Classify is called with 5 and 20, so by hand its entry
// line 2 runs twice, `return 2` (line 3) once and `return 1` (line 6)
// once; the test's two lines (16 and 17) once each.
const coverSource = `Classify(n: int): int {
    if n > 10 {
        return 2
    }
    if n > 0 {
        return 1
    }
    return 0
}

Never(): int {
    return 7
}

test "classify" {
    Assert(Classify(5) == 1)
    Assert(Classify(20) == 2)
}
`

func coverFile(t *testing.T, rep *TestReport, file string) CoverFile {
	t.Helper()
	if rep.Cover == nil {
		t.Fatal("no coverage report")
	}
	for _, f := range rep.Cover.Files {
		if f.File == file {
			return f
		}
	}
	t.Fatalf("no coverage of %s: %+v", file, rep.Cover.Files)
	return CoverFile{}
}

// TestMkTestCoverHandCounted is the hand-counted example: mk test --cover
// reads the counters.
func TestMkTestCoverHandCounted(t *testing.T) {
	root := testPkg(t, map[string]string{"main.mk": coverSource})
	rep := mkTest(t, Config{Root: root}, TestOptions{Cover: true})
	if results(rep) != "main classify pass" {
		t.Fatalf("results: %s", results(rep))
	}
	f := coverFile(t, rep, "main.mk")
	for line, want := range map[int]int64{2: 2, 3: 1, 6: 1} {
		if got, ok := f.Counts[line]; !ok || got != want {
			t.Errorf("line %d: count %d (present %v), want %d; all %v", line, got, ok, want, f.Counts)
		}
	}
	// The test block's lines are in no total (red before: 16 and 17 were
	// counted).
	for _, l := range []int{16, 17} {
		if _, ok := f.Counts[l]; ok {
			t.Errorf("test-block line %d is counted: %v", l, f.Counts)
		}
	}
	for _, r := range rep.Cover.Files {
		if strings.HasPrefix(r.File, "mk-test/") {
			t.Errorf("the generated runner is in the report: %s", r.File)
		}
	}
	var text, lcov strings.Builder
	if err := rep.Cover.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	if err := rep.Cover.WriteLcov(&lcov, "C:/pkg"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "coverage: main.mk: ") || !strings.Contains(text.String(), "coverage: total: ") {
		t.Errorf("text:\n%s", text.String())
	}
	for _, w := range []string{"SF:C:/pkg/main.mk\n", "DA:2,2\n", "DA:3,1\n", "end_of_record\n"} {
		if !strings.Contains(lcov.String(), w) {
			t.Errorf("lcov lacks %q:\n%s", w, lcov.String())
		}
	}
}

const coverSpinSource = `Mark(): int {
    return 3
}

Forever(n: int): int {
    var x := n
    for x >= 0 {
        x = (x + 1) % 1000
    }
    return x
}

test "spins" {
    _ = Mark()
    _ = Forever(Mark())
}
`

// TestMkTestCoverKilledChildReports: a child the runner stops on its timeout
// still reports its counts (the driver asks first, std.rt's writer answers),
// where otherwise a killed child would write nothing.
func TestMkTestCoverKilledChildReports(t *testing.T) {
	root := testPkg(t, map[string]string{"main.mk": coverSpinSource})
	rep := mkTest(t, Config{Root: root, TestTimeout: 3 * time.Second}, TestOptions{Cover: true})
	if results(rep) != "main spins timeout" {
		t.Fatalf("results: %s", results(rep))
	}
	// The counts come from the handshake, not a periodic write.
	if !rep.Tests[0].coverAcked {
		t.Error("the child did not answer the request for its counts")
	}
	if rep.Tests[0].Note != "" {
		t.Errorf("note: %s", rep.Tests[0].Note)
	}
	f := coverFile(t, rep, "main.mk")
	if f.Counts[2] != 2 {
		t.Errorf("Mark's line counts %d, want 2: %v", f.Counts[2], f.Counts)
	}
}

// TestMkTestCoverKillRace: a child killed right after it answered the
// request keeps whole counts: a write that truncates, or a writer that
// keeps re-writing after its answer, leaves an empty counts file.
func TestMkTestCoverKillRace(t *testing.T) {
	root := testPkg(t, map[string]string{"main.mk": coverSpinSource})
	cache := t.TempDir()
	rep := mkTest(t, Config{Root: root, CacheDir: cache, TestTimeout: 500 * time.Millisecond}, TestOptions{Cover: true})
	if results(rep) != "main spins timeout" {
		t.Fatalf("results: %s", results(rep))
	}
	var exe string
	_ = filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if d != nil && !d.IsDir() && d.Name() == "mk-test.exe" {
			exe = p
		}
		return nil
	})
	if exe == "" {
		t.Skip("no runner executable in the cache (not a Windows host)")
	}
	img, err := readCoverImage(exe)
	if err != nil || img == nil {
		t.Fatal(err)
	}
	mark := -1
	for _, r := range img.rows {
		if r.key == "main.Mark" {
			mark = r.first
		}
	}
	lost := 0
	const iters = 60
	for k := 0; k < iters; k++ {
		f := filepath.Join(t.TempDir(), "c")
		cmd := exec.Command(exe, "0")
		cmd.Env = append(os.Environ(), img.coverEnv(f))
		prepareChild(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		acked := coverAsk(f)
		killTree(cmd)
		_ = cmd.Wait()
		sum := make([]uint64, img.size/8)
		if err := addCounts(sum, f); err != nil || !acked || mark < 0 || sum[mark] != 2 {
			lost++
			t.Logf("kill %d: acked %v, %v, Mark %v", k, acked, err, sum[max(mark, 0)])
		}
	}
	if lost > 0 {
		t.Errorf("%d of %d killed children lost counts", lost, iters)
	}
}

// TestMkTestCoverOwnModulesOnly: a fetched dependency's rows are not
// reported, so every file is the package's and every lcov SF exists. Red
// before: x@<id>/src/x.mk, on no disk.
func TestMkTestCoverOwnModulesOnly(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/x", "1.0.0", map[string]string{
		"pkg.mk":   depManifest("x"),
		"src/x.mk": "export X(): int {\n    return 7\n}\n",
	})
	x := gs.source("someone/x")
	root := writeTree(t, map[string]string{
		"pkg.mk":      "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("x", x, "1.0.0") + "])\n",
		"src/main.mk": "import x.src\n\nTwice(): int {\n    return 2 * src.X()\n}\n\ntest \"uses x\" {\n    Assert(Twice() == 14)\n}\n",
	})
	cfg := sourceCfg(gs, t.TempDir())
	cfg.Root = root
	rep := mkTest(t, cfg, TestOptions{Cover: true})
	if results(rep) != "src uses x pass" {
		t.Fatalf("results: %s", results(rep))
	}
	if len(rep.Cover.Files) != 1 || rep.Cover.Files[0].File != "src/main.mk" {
		t.Fatalf("files %+v, want src/main.mk alone", rep.Cover.Files)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "main.mk")); err != nil {
		t.Error(err)
	}
}

// TestMkTestCoverRefusals: what coverage cannot serve is a usage error
// naming why: --fuzz --cover, --prop --cover and --std --cover are refused
// rather than printing 0/4, ignoring --cover or counting test blocks only.
func TestMkTestCoverRefusals(t *testing.T) {
	root := testPkg(t, map[string]string{"main.mk": coverSource})
	for _, c := range []struct {
		cfg  Config
		opt  TestOptions
		want string
	}{
		{Config{Root: root}, TestOptions{Cover: true, Fuzz: "classify"}, "--fuzz"},
		{Config{Root: root}, TestOptions{Cover: true, Prop: true}, "--prop"},
		{Config{StdTests: "std.testing"}, TestOptions{Cover: true}, "--std"},
	} {
		c.cfg.Tools = needTools(t)
		c.cfg.CacheDir = t.TempDir()
		s, err := NewSession(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.Test(c.opt)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s with --cover: %v, want a usage error naming it", c.want, err)
		}
	}
}

func TestAddCountsReportsShortFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c")
	if err := os.WriteFile(f, make([]byte, 12), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addCounts(make([]uint64, 4), f); err == nil || !strings.Contains(err.Error(), "short") {
		t.Errorf("a 12-byte file of 4 counters: %v", err)
	}
	if err := addCounts(make([]uint64, 4), f+"x"); err == nil {
		t.Error("a missing file is not reported")
	}
}
