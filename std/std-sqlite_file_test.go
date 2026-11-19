// Why: std.db.sqlite's VFS, lock table and error mapping are Mk code under
// sqlite's C, and only a real file database exercises them together.
// What: TestStdSqlite_File: create, close, reopen and read (vfs.mk), Busy
// between two connections of one process, and the error mapping.
// How: one program (fileProgram) at dev and release, each in a fresh
// directory, against its expected output.

package std

import (
	"runtime"
	"testing"
)

// fileProgram writes a database file, closes it and reads it back through
// a second open; then it holds an exclusive transaction on one connection
// and reads from another (Busy), and runs the statements whose codes the
// error map must keep.
const fileProgram = `import std.db.sqlite

Show(out: mut Writer, what: str, e: sqlite.Error): WriteError!void {
    match e {
        sqlite.Sqlite(code: c) {
            try Print(out, "{what}: sqlite {c}\n", (what: what, c: c))
        }
        sqlite.Busy {
            try Print(out, "{what}: busy\n", (what: what))
        }
        sqlite.Misuse {
            try Print(out, "{what}: misuse\n", (what: what))
        }
        OutOfMemory {
            try Print(out, "{what}: out of memory\n", (what: what))
        }
        Canceled {
            try Print(out, "{what}: canceled\n", (what: what))
        }
        _ {
            Panic("sqlite.Error gained a member this match does not handle")
        }
    }
}

Create(sys: &System, path: str): sqlite.Error!void {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    try db.Exec(sys.io, "create table t(id integer primary key, v text not null); insert into t values(1, 'one'), (2, 'two'), (3, 'three');")
    var ins := try db.Prepare(sys.arena, sys.io, "insert into t(id, v) values(?, ?)")
    defer ins.Close()
    var i: i64 = 4
    for i <= 1000 {
        ins.Reset()
        try ins.BindInt(1, i)
        try ins.BindText(2, "row")
        _ = try ins.Step(sys.io)
        i += 1
    }
}

Count(db: &sqlite.Db, sys: &System): sqlite.Error!i64 {
    var st := try db.Prepare(sys.arena, sys.io, "select count(*), sum(id) from t")
    defer st.Close()
    _ = try st.Step(sys.io)
    return st.Int(0) * 1000000 + st.Int(1)
}

Reopen(sys: &System, out: mut Writer, path: str): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    n := try Count(&db, sys)
    try Print(out, "reopened {n}\n", (n: n))
    var st := try db.Prepare(sys.arena, sys.io, "select v from t where id = 3")
    defer st.Close()
    _ = try st.Step(sys.io)
    try Print(out, "id 3 {v}\n", (v: st.Str(0)))
}

Locks(sys: &System, out: mut Writer, path: str): sqlite.Error|WriteError!void {
    a := try sqlite.Open(sys.arena, sys.io, path)
    defer a.Close()
    try a.Exec(sys.io, "create table t(id integer primary key, v text not null); insert into t values(1, 'x');")
    b := try sqlite.Open(sys.arena, sys.io, path)
    defer b.Close()
    before := try Count(&b, sys)
    try Print(out, "b before {n}\n", (n: before))
    try a.Exec(sys.io, "begin exclusive; insert into t values(2, 'y');")
    match Count(&b, sys) {
        ok(n) {
            try Print(out, "b during {n}\n", (n: n))
        }
        err(e) {
            try Show(out, "b during", e)
        }
    }
    try a.Exec(sys.io, "commit")
    after := try Count(&b, sys)
    try Print(out, "b after {n}\n", (n: after))
}

// PrepareEmpty prepares SQL that holds no statement.
PrepareEmpty(db: &sqlite.Db, sys: &System): sqlite.Error!void {
    s := try db.Prepare(sys.arena, sys.io, "   -- nothing")
    s.Close()
}

// OpenMissing opens a file in a directory that does not exist.
OpenMissing(sys: &System): sqlite.Error!void {
    d := try sqlite.Open(sys.arena, sys.io, "no/such/dir/x.db")
    d.Close()
}

Errors(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    try db.Exec(sys.io, "create table t(id integer primary key, v text not null unique)")
    try db.Exec(sys.io, "insert into t values(1, 'a')")
    db.Exec(sys.io, "selec 1") else e {
        try Show(out, "syntax", e)
    }
    db.Exec(sys.io, "select nope from t") else e {
        try Show(out, "column", e)
    }
    db.Exec(sys.io, "insert into t values(1, 'b')") else e {
        try Show(out, "primary key", e)
    }
    db.Exec(sys.io, "insert into t values(2, 'a')") else e {
        try Show(out, "unique", e)
    }
    db.Exec(sys.io, "insert into t values(3, null)") else e {
        try Show(out, "not null", e)
    }
    PrepareEmpty(&db, sys) else e {
        try Show(out, "empty", e)
    }
    var st := try db.Prepare(sys.arena, sys.io, "select ?")
    defer st.Close()
    st.BindInt(0, 1) else e {
        try Show(out, "bind 0", e)
    }
    st.BindInt(2, 1) else e {
        try Show(out, "bind 2", e)
    }
    try st.BindText(1, "k")
    _ = try st.Step(sys.io)
    try Print(out, "bound {s} {i}\n", (s: st.Str(0), i: st.Int(0)))
    try Print(out, "past end [{s}] {i}\n", (s: st.Str(5), i: st.Int(-1)))
    OpenMissing(sys) else e {
        try Show(out, "open", e)
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    Create(sys, "f.db") else e {
        try Show(mut out, "create", e)
    }
    Reopen(sys, mut out, "f.db") else {
        try Print(mut out, "reopen failed\n")
    }
    Locks(sys, mut out, "l.db") else {
        try Print(mut out, "locks failed\n")
    }
    Errors(sys, mut out) else {
        try Print(mut out, "errors failed\n")
    }
}
`

// fileWant is fileProgram's output. The reopened file opens read-write,
// which this program does not depend on.
const fileWant = `reopened 1000500500
id 3 three
b before 1000001
b during: busy
b after 2000003
syntax: sqlite 1
column: sqlite 1
primary key: sqlite 1555
unique: sqlite 2067
not null: sqlite 1299
empty: misuse
bind 0: sqlite 25
bind 2: sqlite 25
bound k 0
past end [] 0
open: sqlite 14
`

// TestStdSqlite_File runs fileProgram at dev and release, each in a fresh
// directory so the database files start absent.
func TestStdSqlite_File(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		r := newSqliteRoot(t, fileProgram, true, "[]")
		if got := r.run(t, flags...); got != fileWant {
			t.Errorf("file %v:\ngot:\n%s\nwant:\n%s", flags, got, fileWant)
		}
	}
}
