// Why: regression tests for std.db.sqlite defects found by programs written on Mk.
// What: TestExistingDatabaseOpensReadWrite (named after the defect's id).
// How: builds and runs a repro that opens an existing database twice and counts its rows.

package std

import "testing"

// notes01Src is a repro, with the rows counted at the end: a
// database file opened again (in one process, the next run of a server
// alike) is written, and a second connection writes once the first one's
// exclusive transaction is over.
const notes01Src = `import std.db.sqlite
import std.fs

Insert(sys: &System, n: i64): sqlite.Error!void {
    db := try sqlite.Open(sys.arena, sys.io, "reopen.db")
    defer db.Close()
    try db.Exec(sys.io, "create table if not exists t (x integer)")
    var st := try db.Prepare(sys.arena, sys.io, "insert into t (x) values (?1)")
    defer st.Close()
    try st.BindInt(1, n)
    _ = try st.Step(sys.io)
}

Count(sys: &System, path: str): sqlite.Error!i64 {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    var st := try db.Prepare(sys.arena, sys.io, "select count(*) from t")
    defer st.Close()
    _ = try st.Step(sys.io)
    return st.Int(0)
}

TwoConnections(sys: &System): sqlite.Error|WriteError!void {
    fs.Remove(sys.io, "two.db") else {}
    a := try sqlite.Open(sys.arena, sys.io, "two.db")
    defer a.Close()
    try a.Exec(sys.io, "create table t (x integer)")
    b := try sqlite.Open(sys.arena, sys.io, "two.db")
    defer b.Close()
    try a.Exec(sys.io, "begin exclusive")
    b.Exec(sys.io, "insert into t values (1)") else e {
        try sys.Print("while locked: {e}\n", (e))
    }
    try a.Exec(sys.io, "commit")
    b.Exec(sys.io, "insert into t values (2)") else e {
        try sys.Print("after the lock is released: {e}\n", (e))
        return
    }
    try sys.Print("after the lock is released: ok\n")
}

export Main(sys: &System): sqlite.Error|WriteError!void {
    fs.Remove(sys.io, "reopen.db") else {}
    try Insert(sys, 1)
    Insert(sys, 2) else e {
        try sys.Print("second open: {e}\n", (e))
    }
    n := try Count(sys, "reopen.db")
    try sys.Print("reopen.db rows {n}\n", (n))
    try TwoConnections(sys)
    m := try Count(sys, "two.db")
    try sys.Print("two.db rows {m}\n", (m))
}
`

// A database file that exists already opens read-write
// (std.sys's open-or-create without truncation), so a second open writes
// it, and a second connection writes once the lock is released; only the
// write made while the other connection holds its exclusive transaction
// is Busy.
func TestExistingDatabaseOpensReadWrite(t *testing.T) {
	r := newSqliteRoot(t, notes01Src, true, "[]")
	got := r.run(t)
	want := "reopen.db rows 2\nwhile locked: Busy\nafter the lock is released: ok\ntwo.db rows 1\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
