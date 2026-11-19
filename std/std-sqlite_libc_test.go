// Why: std.libc is the C-callable layer sqlite runs on, written in Mk, so
// its string and memory functions need a check against a model.
// What: TestStdSqlite_Libc against a Go model, TestStdSqlite_NeedsCImport
// (E0202 without a sqlite3 C import), and TestStdSqlite_Notes.
// How: Mk programs call each function on every case at dev and release and
// print results that the Go side compares with libcModel.

package std

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// libcCase is one string case: s and t are NUL-terminated by the program,
// c is the byte searched for.
type libcCase struct {
	s, t string
	c    byte
}

var libcCases = []libcCase{
	{"", "", 0}, {"", "a", 'a'}, {"a", "", 'a'}, {"abc", "abc", 'c'}, {"abc", "abd", 'b'},
	{"abd", "abc", 'z'}, {"ab", "abc", 0}, {"abcabc", "ba", 'b'}, {"xyz", "abc", 'y'},
	{"hello world", " ", 'o'}, {"héllo", "é", 0xA9}, {"ÿÿa", "ÿ", 0xBF}, {"aaaa", "a", 'a'},
	{"mississippi", "is", 's'}, {"select * from t", " *", 'f'}, {"x", "xyzzy", 'x'},
}

// libcModel is the Go reading of one case: strlen, strspn, strcspn,
// strchr, strrchr (offsets, -1 for null), sign of strcmp, sign of
// strncmp(3), memchr over len(s).
func libcModel(k libcCase) string {
	s, t := []byte(k.s), []byte(k.t)
	in := func(set []byte, b byte) bool { return strings.IndexByte(string(set), b) >= 0 }
	spn, cspn := 0, 0
	for spn < len(s) && in(t, s[spn]) {
		spn++
	}
	for cspn < len(s) && !in(t, s[cspn]) {
		cspn++
	}
	z := append(append([]byte{}, s...), 0) // with its NUL, as C sees it
	chr, rchr := strings.IndexByte(string(z), k.c), strings.LastIndexByte(string(z), k.c)
	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		}
		return 0
	}
	cmp := func(a, b []byte, n int) int {
		a, b = append(append([]byte{}, a...), 0), append(append([]byte{}, b...), 0)
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return sign(int(a[i]) - int(b[i]))
			}
			if a[i] == 0 {
				return 0
			}
		}
		return 0
	}
	mem := strings.IndexByte(k.s, k.c)
	return fmt.Sprintf("%d %d %d %d %d %d %d %d", len(s), spn, cspn, chr, rchr, cmp(s, t, 1<<30), cmp(s, t, 3), mem)
}

// libcProgram calls each libc function on every case and runs the
// allocator churn of libcAlloc.
func libcProgram() string {
	var b strings.Builder
	b.WriteString(`import std.libc
import std.raw

CStr(a: &Arena, s: str): *u8 {
    buf := a.Slice[u8](s.Len() + 1) else {
        return raw.Null[u8]()
    }
    unsafe "buf holds s and a NUL" {
        raw.Copy(raw.DataMut(buf), raw.StrData(s), s.Len())
        return raw.DataMut(buf)
    }
}

Off(base: *u8, p: *u8): int {
    if raw.IsNull(p) {
        return -1
    }
    return Trunc[int](raw.Addr(p) - raw.Addr(base))
}

Sign(n: i32): int {
    if n < 0 {
        return -1
    }
    if n > 0 {
        return 1
    }
    return 0
}

Case(out: mut Writer, a: &Arena, s: str, t: str, c: i32): WriteError!void {
    p := CStr(a, s)
    q := CStr(a, t)
    unsafe "libc on NUL-terminated buffers of the arena" {
        try Print(out, "{l} {spn} {cspn} {chr} {rchr} {cmp} {ncmp} {mem}\n", (l: Trunc[int](libc.strlen(p)), spn: Trunc[int](libc.strspn(p, q)),
            cspn: Trunc[int](libc.strcspn(p, q)), chr: Off(p, libc.strchr(p, c)), rchr: Off(p, libc.strrchr(p, c)),
            cmp: Sign(libc.strcmp(p, q)), ncmp: Sign(libc.strncmp(p, q, 3)), mem: Off(p, libc.memchr(p, c, Trunc[uint](s.Len())))))
    }
}

// Alloc grows and shrinks blocks with a byte pattern and checks it; it
// prints the failures it found (0 when the allocator is right). The null
// results (a huge size, an overflowing calloc) and malloc(0) are checked
// from C in TestStdSqlite_LibcFromC: Mk code calling a function named
// malloc gets LLVM's library semantics at release, which may elide an
// allocation whose result is only compared with null.
Alloc(): int {
    var bad := 0
    unsafe "the blocks are libc's and used within their sizes" {
        var p := libc.malloc(1)
        raw.Write(p, 7)
        var n := 1
        for n < 70000 {
            m := n * 3 + 5
            q := libc.realloc(p, Trunc[uint](m))
            if raw.IsNull(q) {
                return 1000
            }
            var i := 0
            for i < n && i < 4096 {
                if raw.Read(raw.Offset(q, i)) != Trunc[u8](i * 7 + 7) {
                    bad += 1
                }
                i += 1
            }
            i = 0
            for i < m {
                raw.Write(raw.Offset(q, i), Trunc[u8](i * 7 + 7))
                i += 1
            }
            p = q
            n = m
        }
        s := libc.realloc(p, 10)
        var j := 0
        for j < 10 {
            if raw.Read(raw.Offset(s, j)) != Trunc[u8](j * 7 + 7) {
                bad += 1
            }
            j += 1
        }
        if (raw.Addr(s) & 15) != 0 {
            bad += 1
        }
        libc.free(s)
        libc.free(raw.Null[u8]())
        z := libc.calloc(1000, 8)
        var k := 0
        for k < 8000 {
            if raw.Read(raw.Offset(z, k)) != 0 {
                bad += 1
            }
            k += 1
        }
        libc.free(z)
        if libc.fabs(-2.5) != 2.5 || libc.fabs(3.0) != 3.0 {
            bad += 1
        }
    }
    return bad
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
`)
	for _, k := range libcCases {
		fmt.Fprintf(&b, "    try Case(mut out, sys.arena, %s, %s, %d)\n", strconv.Quote(k.s), strconv.Quote(k.t), k.c)
	}
	b.WriteString("    try Print(mut out, \"alloc {n}\\n\", (n: Alloc()))\n}\n")
	return b.String()
}

// TestStdSqlite_Libc runs every std.libc function against the Go model,
// at dev and release.
func TestStdSqlite_Libc(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the libc program is built for x86_64-windows")
	}
	var want strings.Builder
	for _, k := range libcCases {
		want.WriteString(libcModel(k) + "\n")
	}
	want.WriteString("alloc 0\n")
	r := newSqliteRoot(t, libcProgram(), false, `["."]`)
	for _, flags := range [][]string{nil, {"--release"}} {
		if got := r.run(t, flags...); got != want.String() {
			t.Errorf("libc %v:\ngot:\n%s\nwant:\n%s", flags, got, want.String())
		}
	}
}

// TestStdSqlite_NeedsCImport: std.db.sqlite in a package that declares no
// sqlite3 C import is E0202 with reason needs-cimport.
func TestStdSqlite_NeedsCImport(t *testing.T) {
	src := "import std.db.sqlite\n\nexport Main(sys: &System): sqlite.Error!void {\n    db := try sqlite.Open(sys.arena, sys.io, \":memory:\")\n    db.Close()\n}\n"
	r := newSqliteRoot(t, src, false, "[]")
	out, st := r.mk(t, "check", "--target=x86_64-windows", "--json", ".")
	if st == 0 || !strings.Contains(out, "E0202") || !strings.Contains(out, "needs-cimport") {
		t.Fatalf("want E0202 needs-cimport, got exit %d:\n%s", st, out)
	}
}

// notesProgram is the database half of doc/examples.md's notes
// service: a table, inserts through BindInt and BindText, a select by id
// whose text is copied out with Arena.Dup, a miss, Reset and rebind, and
// the errors of a bad statement.
const notesProgram = `import std.db.sqlite

error NotFound(id: i64)

struct Note {
    id: i64
    title: str
    body: str
}

type LoadError = NotFound | sqlite.Error | OutOfMemory

LoadNote(db: &sqlite.Db, a: &Arena, io: Io, id: i64): LoadError!Note from a {
    var stmt := try db.Prepare(a, io, "select title, body from notes where id = ?")
    defer stmt.Close()
    try stmt.BindInt(1, id)
    found := try stmt.Step(io)
    if !found {
        fail NotFound(id: id)
    }
    title := try a.Dup(stmt.Str(0))
    body := try a.Dup(stmt.Str(1))
    return Note(id: id, title: title, body: body)
}

Run(sys: &System, out: mut Writer, path: str): sqlite.Error|OutOfMemory|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    try db.Exec(sys.io, "create table notes(id integer primary key, title text not null, body text); insert into notes values(1, 'first', 'hello');")
    var ins := try db.Prepare(sys.arena, sys.io, "insert into notes(id, title, body) values(?, ?, ?)")
    defer ins.Close()
    try ins.BindInt(1, 2)
    try ins.BindText(2, "second")
    try ins.BindText(3, "wörld")
    _ = try ins.Step(sys.io)
    ins.Reset()
    try ins.BindInt(1, 3)
    try ins.BindText(2, "third")
    try ins.BindText(3, "")
    _ = try ins.Step(sys.io)
    var id: i64 = 1
    for id <= 4 {
        n := LoadNote(&db, sys.arena, sys.io, id) else e {
            match e {
                NotFound {
                    try Print(out, "{id} not found\n", (id: id))
                }
                _ {
                    try Print(out, "{id} error\n", (id: id))
                }
            }
            id += 1
            continue
        }
        try Print(out, "{id} {t} [{b}]\n", (id: n.id, t: n.title, b: n.body))
        id += 1
    }
    db.Exec(sys.io, "selec 1") else e {
        match e {
            sqlite.Sqlite {
                try Print(out, "syntax error\n")
            }
            _ {
                try Print(out, "other error\n")
            }
        }
    }
    ins.Reset()
    try ins.BindInt(1, 1)
    try ins.BindText(2, "dup")
    _ = ins.Step(sys.io) else e {
        match e {
            sqlite.Sqlite {
                try Print(out, "constraint\n")
            }
            _ {
                try Print(out, "other\n")
            }
        }
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    Run(sys, mut out, ":memory:") else {
        try Print(mut out, "failed\n")
    }
}
`

// TestStdSqlite_Notes builds the notes program against the amalgamation
// and runs it at dev and release, on an in-memory database.
func TestStdSqlite_Notes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the notes program is built for x86_64-windows")
	}
	r := newSqliteRoot(t, notesProgram, true, "[]")
	want := "1 first [hello]\n2 second [wörld]\n3 third []\n4 not found\nsyntax error\nconstraint\n"
	for _, flags := range [][]string{nil, {"--release"}} {
		if got := r.run(t, flags...); got != want {
			t.Errorf("notes %v:\ngot:\n%s\nwant:\n%s", flags, got, want)
		}
	}
}
