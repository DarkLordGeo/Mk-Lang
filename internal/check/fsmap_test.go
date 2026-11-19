// Why: fs.Map maps a file into memory that another process may change, so
// calling it needs `unsafe`.
// What: TestWireFsMapNeedsUnsafe.
// How: a call of std.fs's free function Map outside `unsafe` is E1101 with
// the operation `fs.Map`, as str.FromC's is, and so is naming it as a value;
// inside `unsafe` it checks clean, and neither a method named Map nor a
// non-std module's Map is caught.

package check

import (
	"testing"

	"mk/internal/diag"
)

// wire8FsStub is the std.fs surface the cases need: Map's parameters are
// the reference's without its Io; its result is a plain int.
const wire8FsStub = "export struct File {\n\tfd: int\n}\n\n" +
	"export Map(f: &File, offset: i64, len: int): int {\n\treturn len\n}\n\n" +
	"export File.Map(&self): int {\n\treturn self.fd\n}\n"

func TestWireFsMapNeedsUnsafe(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"call outside unsafe", "F(f: &fs.File): int {\n\tm := fs.Map(f, 0, 16)\n\treturn m\n}\n", "E1101@4:7"},
		{"call in unsafe", "F(f: &fs.File): int {\n\tunsafe \"no other process writes the file\" {\n\t\treturn fs.Map(f, 0, 16)\n\t}\n}\n", ""},
		{"nested in an expression", "F(f: &fs.File): int {\n\treturn fs.Map(f, 0, fs.Map(f, 0, 1))\n}\n", "E1101@4:22 E1101@4:9"},
		{"method named Map", "F(f: &fs.File): int {\n\treturn f.Map()\n}\n", ""},
		// A call through a fn value is a call of fs.Map that the
		// call rule never sees, so naming it outside unsafe is E1101 at the
		// name, as for a cpu or naked function.
		{"named as a value", "F(f: &fs.File): int {\n\tg := fs.Map\n\treturn g(f, 0, 16)\n}\n", "E1101@4:7"},
		{"named in unsafe", "F(f: &fs.File): int {\n\tunsafe \"no other process writes the file\" {\n\t\tg := fs.Map\n\t\treturn g(f, 0, 16)\n\t}\n}\n", ""},
		{"named as an argument", "Apply(m: fn(&fs.File, i64, int): int, f: &fs.File): int {\n\treturn m(f, 0, 1)\n}\n\nF(f: &fs.File): int {\n\treturn Apply(fs.Map, f)\n}\n", "E1101@8:15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("std.fs", opt{std: true}, wire8FsStub).wantClean(t)
			b := checkBody(t, w, tc.want, "import std.fs\n\n"+tc.src)
			for _, d := range b.bodyDiags(t) {
				if d.Code == diag.E1101 && fact(d, "operation") != "fs.Map" {
					t.Errorf("E1101 operation %q, want fs.Map", fact(d, "operation"))
				}
			}
		})
	}
	t.Run("a user module's Map", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		w.add("fs", opt{}, wire8FsStub).wantClean(t)
		checkBody(t, w, "", "import fs\n\nF(f: &fs.File): int {\n\treturn fs.Map(f, 0, 16)\n}\n")
	})
	t.Run("a user module's Map named", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		w.add("fs", opt{}, wire8FsStub).wantClean(t)
		checkBody(t, w, "", "import fs\n\nF(f: &fs.File): int {\n\tg := fs.Map\n\treturn g(f, 0, 16)\n}\n")
	})
}
