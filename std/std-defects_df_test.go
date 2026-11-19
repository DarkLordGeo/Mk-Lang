// Why: regression tests for std defects found by programs written on Mk.
// What: one test per defect, named after what it checks.
// How: each builds and runs the program's minimal repro against this std
// (dfRun, dfWant) and checks its output and exit status.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dfRun writes files (path to text) into a fresh directory, runs mk with
// args there against this std, and returns the combined output and status.
func dfRun(t *testing.T, files map[string]string, args ...string) (string, int) {
	t.Helper()
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, status := mkCmd(t, dir, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	return strings.ReplaceAll(out, "\r\n", "\n"), status
}

// dfWant runs main.mk with mk run and wants exit 0 and stdout ending in want.
func dfWant(t *testing.T, files map[string]string, want string) {
	t.Helper()
	out, status := dfRun(t, files, "run", "main.mk")
	if status != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("mk run main.mk: exit %d, output\n%s\nwant exit 0 and output ending in %q", status, out, want)
	}
}

// A name Dir.Next copied into the arena is kept in a list in
// that arena with no second copy: Path.Str and AsBytes are from
// self.bytes, the arena's bytes, not from the Entry local.
func TestPathStrBorrowsTheBytes(t *testing.T) {
	dfWant(t, map[string]string{
		"d/one.txt": "1",
		"d/two.txt": "2",
		"main.mk": `import std.fs

Names(a: &Arena, io: Io, dir: str): fs.Error|OutOfMemory!List[str] from a {
    var out := List[str].New(a)
    var d := try fs.Dir.Open(io, dir)
    defer d.Close()
    for {
        entry := try d.Next(a, io) else {
            break
        }
        name := entry.name.Str() else {
            continue
        }
        try out.Push(name)
        bytes := entry.name.AsBytes()
        _ = bytes
    }
    return out
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    names := try Names(sys.arena, sys.io, "d")
    var total := 0
    for n in names {
        total += n.Len()
    }
    try sys.Print("{n} {total}\n", (n: names.Len(), total))
}
`,
	}, "2 14\n")
}

// gen json.Codec of a struct holding a List of a codec
// record with a str field (FromJson copies each
// str into a and is from a).
func TestCodecListOfStrRecords(t *testing.T) {
	dfWant(t, map[string]string{"main.mk": `import std.json

struct Item {
    export name: str
}

struct Both {
    export items: List[Item]
}

gen json.Codec(Item)
gen json.Codec(Both)

export Main(sys: &System): json.Error|WriteError!void {
    v := try json.Parse(sys.arena, "{\"items\": [{\"name\": \"a\"}, {\"name\": \"bc\"}]}")
    b := try Both.FromJson(sys.arena, &v)
    var out := sys.Stdout()
    try b.FormatJson(mut out)
    try sys.Print("\n")
}
`}, "{\"items\":[{\"name\":\"a\"},{\"name\":\"bc\"}]}\n")
}

// gen json.Codec of a struct with a []T field of a codec type.
func TestCodecSliceOfCodecRecords(t *testing.T) {
	dfWant(t, map[string]string{"main.mk": `import std.json

struct Item {
    export name: str
}

gen json.Codec(Item)

struct Page {
    export items: []Item
    export total: int
}

gen json.Codec(Page)

export Main(sys: &System): json.Error|WriteError!void {
    v := try json.Parse(sys.arena, "{\"items\": [{\"name\": \"a\"}], \"total\": 1}")
    p := try Page.FromJson(sys.arena, &v)
    try sys.Print("{n} {name}\n", (n: p.total, name: p.items[0].name))
}
`}, "1 a\n")
}

// A --pgo-gen program writes its profile to a temporary
// name and renames it when whole, so the directory never holds a partial
// <name>-<pid>.mkprof (a child killed while it exits left a 0-byte one,
// which mk profile merge refused). Here: the run leaves exactly the named
// profile, whole, and no temporary file.
func TestProfileRenamedWhenWhole(t *testing.T) {
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte("export Main(sys: &System): WriteError!void {\n    try sys.Print(\"hi\\n\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "prof.exe")
	if out, st := mkCmd(t, root, stdDir, "build", "--release", "--pgo-gen", "-o", exe, "main.mk"); st != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build --pgo-gen: exit %d\n%s", st, out)
	}
	dir := t.TempDir()
	out, code := cfaultRun(t, exe, "MK_PROFILE_DIR="+dir)
	if code != 0 || out != "hi\n" {
		t.Fatalf("exit %d, output %q", code, out)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || !strings.HasPrefix(ents[0].Name(), "prof-") || !strings.HasSuffix(ents[0].Name(), ".mkprof") {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("profile directory holds %v, want one prof-<pid>.mkprof", names)
	}
	merged := filepath.Join(t.TempDir(), "merged.mkprof")
	if out, st := mkCmd(t, root, stdDir, "profile", "merge", "-o", merged, filepath.Join(dir, ents[0].Name())); st != 0 {
		t.Fatalf("mk profile merge: exit %d\n%s", st, out)
	}
}

// The code the compiler generates around Main writes
// the error line itself, "error: " then the display name's module prefix
// then the error as {e} formats it, and calls std.rt's MainErrorWritten, so
// Finish writes no line of its own and the process exits 1.
func TestMainErrorLineHasItsFields(t *testing.T) {
	out, status := dfRun(t, map[string]string{"main.mk": "error Bad(code: i64)\n\nexport Main(sys: &System): Bad!void {\n    fail Bad(code: 7)\n}\n"}, "run", "main.mk")
	if status != 1 || !strings.Contains(out, "error: main.Bad(code: 7)\n") || strings.Contains(out, "error: main.Bad\n") {
		t.Errorf("mk run main.mk: exit %d, output\n%s\nwant exit 1 and the one line error: main.Bad(code: 7)", status, out)
	}
}
