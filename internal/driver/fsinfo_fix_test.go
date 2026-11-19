// Why: fs.Info's 1.1 fields were replaced by kind and modified, and mk fix
// must move user code across without breaking it.
// What: TestFsInfoFixRound and TestFsInfoFixLeavesWrites over
// the round in fix_fsinfo.go.
// How: runs mk fix over a package with a stand-in std (fsInfoStd) in both
// states, old fields present or gone; each rewrite is proven by
// recompilation and a second run changes nothing.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/format"
)

// fsInfoStd is the stand-in std with std.fs's Info as 1.2 declares it,
// with 1.1's two fields beside the new ones when old.
func fsInfoStd(t *testing.T, old bool) string {
	t.Helper()
	files := fakeStdFiles()
	files["time/time.mk"] = "export struct Time {\n    nanos: i64\n}\n\nexport Time.UnixNanos(self): i64 {\n    return self.nanos\n}\n\nexport FromUnix(n: i64): Time {\n    return Time(nanos: n)\n}\n"
	oldFields, oldInit := "", ""
	if old {
		oldFields = "    export is_dir: bool\n    export modified_unix_nanos: i64\n"
		oldInit = "is_dir: true, modified_unix_nanos: 7, "
	}
	files["fs/fs.mk"] = "import std.time\n\nexport ReadText(): int {\n    return 1\n}\n\nexport open enum Kind {\n    File\n    Dir\n}\n\n" +
		"export struct Info {\n" + oldFields + "    export size: i64\n    export modified: time.Time\n    export kind: Kind\n}\n\n" +
		"export Stat(): Info {\n    return Info(" + oldInit + "size: 1, modified: time.FromUnix(7), kind: Kind.Dir)\n}\n"
	return writeTree(t, files)
}

// The package: main reads both fields, under `!`, `==` and `&&`, through a
// reference, and as an argument; util reads is_dir with no import of std.fs.
const fsInfoMain = `import std.fs
import u = src.util

Score(i: &fs.Info, j: fs.Info): i64 {
    var n: i64 = 0
    if i.is_dir {
        n += 1
    }
    if !j.is_dir && i.modified_unix_nanos > 0 {
        n += 2
    }
    same := i.is_dir == j.is_dir
    if same {
        n += Twice(j.modified_unix_nanos)
    }
    return n + u.Dirs()
}

Twice(x: i64): i64 {
    return x * 2
}
`

const fsInfoUtil = `import h = src.help

export Dirs(): i64 {
    d := h.Get()
    if d.is_dir {
        return 1
    }
    return 0
}
`

const fsInfoHelp = `import std.fs

export Get(): fs.Info {
    return fs.Stat()
}
`

const fsInfoMainFixed = `    if i.kind == fs.Kind.Dir {
        n += 1
    }
    if j.kind != fs.Kind.Dir && i.modified.UnixNanos() > 0 {
        n += 2
    }
    same := (i.kind == fs.Kind.Dir) == (j.kind == fs.Kind.Dir)
    if same {
        n += Twice(j.modified.UnixNanos())
    }`

func fsInfoFix(t *testing.T, std, dir string) *FixResult {
	t.Helper()
	s, err := NewSession(Config{Root: dir, StdDir: std, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFsInfoFixRound: in both of std's states the round rewrites
// every read, the util module gains its import, the package then checks
// clean, and a second mk fix changes nothing.
func TestFsInfoFixRound(t *testing.T) {
	for _, old := range []bool{true, false} {
		name := "fields removed"
		if old {
			name = "fields beside"
		}
		t.Run(name, func(t *testing.T) {
			std := fsInfoStd(t, old)
			dir := testPkg(t, map[string]string{"pkg.mk": depManifest("app"), "src/main.mk": fsInfoMain,
				"src/util/util.mk": fsInfoUtil, "src/help/help.mk": fsInfoHelp})
			if !old {
				r := check(t, Config{Root: dir, StdDir: std})
				if n := len(r.Report.Diags); n == 0 || r.Report.Diags[0].Diag.Code != diag.E0203 {
					t.Fatalf("before the round: want E0203s, got:\n%s", r.Report.Text(r.Sources))
				}
			}
			res := fsInfoFix(t, std, dir)
			if res.Stopped != "clean" {
				t.Fatalf("stopped %q:\n%s", res.Stopped, res.Final.Report.Text(res.Final.Sources))
			}
			main := readFile(t, filepath.Join(dir, "src", "main.mk"))
			if !strings.Contains(main, fsInfoMainFixed) {
				t.Errorf("main.mk:\n%s", main)
			}
			util := readFile(t, filepath.Join(dir, "src", "util", "util.mk"))
			if !strings.Contains(util, "import h = src.help\nimport std.fs\n") || !strings.Contains(util, "if d.kind == fs.Kind.Dir {") {
				t.Errorf("util.mk:\n%s", util)
			}
			// The rewrite is mk fmt's layout already: the import line and the
			// parentheses survive formatting.
			for _, f := range []string{"src/main.mk", "src/util/util.mk"} {
				text := readFile(t, filepath.Join(dir, filepath.FromSlash(f)))
				out, err := format.Source(f, []byte(text))
				if err != nil || string(out) != text {
					t.Errorf("%s is not a fixed point of mk fmt (%v):\n%s", f, err, out)
				}
			}
			if r := check(t, Config{Root: dir, StdDir: std}); len(r.Report.Diags) != 0 {
				t.Errorf("after the round:\n%s", r.Report.Text(r.Sources))
			}
			again := fsInfoFix(t, std, dir)
			if len(again.Rounds) != 0 || len(again.Files) != 0 || readFile(t, filepath.Join(dir, "src", "main.mk")) != main {
				t.Errorf("not idempotent: %d rounds, files %v", len(again.Rounds), again.Files)
			}
		})
	}
}

// TestFsInfoFixLeavesWrites: an assignment to a 1.1 field and a
// `mut` or `&` of one are writes the round leaves, so the file's reads are
// still rewritten and the fix stops clean (a rewrite of the write did not
// check, which dropped the file's whole candidate).
func TestFsInfoFixLeavesWrites(t *testing.T) {
	std := fsInfoStd(t, true)
	dir := testPkg(t, map[string]string{"pkg.mk": depManifest("app"), "src/main.mk": `import std.fs

Describe(i: &fs.Info): int {
    if i.is_dir {
        return 1
    }
    return 0
}

Clear(i: mut fs.Info) {
    i.is_dir = false
    (i.modified_unix_nanos) += 1
    Zero(mut i.modified_unix_nanos)
    Look(&i.is_dir)
}

Zero(x: mut i64) {
    *x = 0
}

Look(b: &bool) {
}
`})
	res := fsInfoFix(t, std, dir)
	if res.Stopped != "clean" {
		t.Fatalf("stopped %q:\n%s", res.Stopped, res.Final.Report.Text(res.Final.Sources))
	}
	main := readFile(t, filepath.Join(dir, "src", "main.mk"))
	for _, want := range []string{"    if i.kind == fs.Kind.Dir {\n", "    i.is_dir = false\n", "    (i.modified_unix_nanos) += 1\n",
		"    Zero(mut i.modified_unix_nanos)\n", "    Look(&i.is_dir)\n"} {
		if !strings.Contains(main, want) {
			t.Errorf("main.mk lacks %q:\n%s", want, main)
		}
	}
}
