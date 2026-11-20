// Why: std.core is the prelude module, the one module that does not see the
// prelude (seesCore false), so mk test --std on it is the selection most
// likely to differ from the others.
// What: coreProbe and TestMkTestStdCore.
// How: writes a core/tests_*.mk file into a copy of std, runs mk test
// --std=std.core with and without -run, and checks that a package's mk test
// never runs std's blocks.

package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"mk/std"
)

// stdTree writes the embedded std to a fresh directory, then the files of
// extra (slash paths relative to std/), and returns the directory.
func stdTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(std.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(std.FS, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, src := range extra {
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// coreProbe is a core/tests_*.mk file as std's own tests are written: a
// private helper of std.core, a block that uses it, a block that takes the
// System and uses its heap, and a block that must fail.
const coreProbe = `// A probe of mk test --std=std.core (driver lane, stdcore_test.go).

ZzProbeTwo(): int {
    return 2
}

test "ZzProbe-Private uses a private std.core function" {
    Assert(ZzProbeTwo() + 1 == 3)
}

test "ZzProbe-Heap uses the System's heap" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    try xs.Push(sys.heap.Alloc(), 7)
    Assert(xs.Len() == 1)
}

test "ZzProbe-Fails must fail" {
    Assert(ZzProbeTwo() == 3)
}
`

// TestMkTestStdCore: mk test --std=std.core runs the test blocks of a
// core/tests_*.mk file, private helpers and the System included, and
// reports a failing one as a failure; -run selects among them; and the
// same file is never run by a package's mk test (std's blocks are not the
// package's).
func TestMkTestStdCore(t *testing.T) {
	dir := stdTree(t, map[string]string{"core/tests_zzprobe.mk": coreProbe})
	// One cache for the three runs: they build the same std copy, and only
	// the selection or the root differs, so the second and third reuse
	// what the first built (a stale entry would show as a wrong selection).
	cache := t.TempDir()
	rep := mkTest(t, Config{StdTests: "std.core", StdDir: dir, CacheDir: cache}, TestOptions{Run: "ZzProbe-"})
	want := map[string]string{
		"ZzProbe-Private uses a private std.core function": "pass",
		"ZzProbe-Heap uses the System's heap":              "pass",
		"ZzProbe-Fails must fail":                          "panic",
	}
	if len(rep.Tests) != len(want) {
		t.Fatalf("%d tests, want %d:\n%s", len(rep.Tests), len(want), results(rep))
	}
	for _, tc := range rep.Tests {
		if tc.Module != "std.core" {
			t.Errorf("%s: module %s, want std.core", tc.Name, tc.Module)
		}
		if w, ok := want[tc.Name]; !ok || tc.Result != w {
			t.Errorf("%s: %s, want %q\n%s", tc.Name, tc.Result, w, tc.Output)
		}
	}
	if rep.ExitStatus() != 1 || rep.Passed != 2 || rep.Failed != 1 {
		t.Errorf("passed %d failed %d exit %d, want 2, 1, 1", rep.Passed, rep.Failed, rep.ExitStatus())
	}

	one := mkTest(t, Config{StdTests: "std.core", StdDir: dir, CacheDir: cache}, TestOptions{Run: "ZzProbe-Private"})
	if len(one.Tests) != 1 || one.Tests[0].Result != "pass" || one.ExitStatus() != 0 {
		t.Errorf("-run ZzProbe-Private:\n%s", results(one))
	}

	pkg := testPkg(t, map[string]string{"t.mk": "test \"own\" {\n    Assert(true)\n}\n"})
	own := mkTest(t, Config{Root: filepath.Join(pkg, "t.mk"), StdDir: dir, CacheDir: cache}, TestOptions{})
	if len(own.Tests) != 1 || own.Tests[0].Module == "std.core" {
		t.Errorf("a package's mk test ran std's blocks:\n%s", results(own))
	}
}
