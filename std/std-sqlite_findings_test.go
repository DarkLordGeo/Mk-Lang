// Why: past bugs in std.db.sqlite's VFS, error mapping and lock must stay
// fixed.
// What: a rollback larger than the page cache, a first transaction's
// rollback, VACUUM and TRUNCATE journals, two spellings of one file, a path
// with a NUL, SQLITE_LOCKED, non-UTF-8 text, TOOBIG, NOMEM, Canceled, the
// lock under contention, and directory syncs on Linux.
// How: one program per area, each on its own file, plus checks of the files
// left behind; the directory syncs are counted under strace in WSL.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// findingsProgram runs one function per regression, each on its own file.
const findingsProgram = `import std.db.sqlite

Show(out: mut Writer, what: str, e: sqlite.Error|WriteError): WriteError!void {
    match e {
        WriteError {
            try Print(out, "{what}: write error\n", (what: what))
        }
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

// One returns the integer in column 0 of the first row of sql.
One(db: &sqlite.Db, sys: &System, sql: str): sqlite.Error!i64 {
    var st := try db.Prepare(sys.arena, sys.io, sql)
    defer st.Close()
    _ = try st.Step(sys.io)
    return st.Int(0)
}

// Text prints the text in column 0 of the first row of sql.
Text(db: &sqlite.Db, sys: &System, out: mut Writer, what: str, sql: str): sqlite.Error|WriteError!void {
    var st := try db.Prepare(sys.arena, sys.io, sql)
    defer st.Close()
    _ = try st.Step(sys.io)
    try Print(out, "{what} {s}\n", (what: what, s: st.Str(0)))
}

// Rows inserts n rows of 200 bytes of text in one statement.
const Rows = "with recursive c(x) as (select 1 union all select x + 1 from c where x < ?) insert into t(v) select hex(zeroblob(100)) from c"

Fill(db: &sqlite.Db, sys: &System, n: i64): sqlite.Error!void {
    var st := try db.Prepare(sys.arena, sys.io, Rows)
    defer st.Close()
    try st.BindInt(1, n)
    _ = try st.Step(sys.io)
}

// SQL-4: a rollback of a transaction larger than the page cache (20 MB
// against sqlite's 2 MB default) restores the database, deletes the
// journal, and the database reopens with the rows from before.
RollbackBig(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, "r.db")
    defer db.Close()
    try db.Exec(sys.io, "create table t(id integer primary key, v text not null)")
    try Fill(&db, sys, 10)
    try db.Exec(sys.io, "begin")
    try Fill(&db, sys, 100000)
    n := try One(&db, sys, "select count(*) from t")
    try db.Exec(sys.io, "rollback")
    m := try One(&db, sys, "select count(*) from t")
    try Print(out, "rollback {n} -> {m}\n", (n: n, m: m))
    // The file keeps the rolled-back pages past the header's page count
    // (FTruncate); the pages allocated now are written over them.
    try db.Exec(sys.io, "insert into t(v) values('after')")
    try Fill(&db, sys, 5000)
    try Text(&db, sys, out, "integrity", "pragma integrity_check")
}

Reopen(sys: &System, out: mut Writer, path: str): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    n := try One(&db, sys, "select count(*) from t")
    try Print(out, "reopened {path} {n}\n", (path: path, n: n))
    try Text(&db, sys, out, "integrity", "pragma integrity_check")
    // An existing file opens read-write: the write succeeds and
    // prints nothing.
    db.Exec(sys.io, "insert into t(v) values('x')") else e {
        try Show(out, "write after reopen", e)
    }
}

// SQL-4: the rollback of a database's first transaction, spilled to the
// file, truncates the file to zero.
RollbackFirst(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, "z.db")
    defer db.Close()
    try db.Exec(sys.io, "begin; create table t(id integer primary key, v text not null)")
    try Fill(&db, sys, 100000)
    try db.Exec(sys.io, "rollback")
    n := try One(&db, sys, "select count(*) from sqlite_master")
    try Print(out, "first rollback tables {n}\n", (n: n))
}

// SQL-4: VACUUM after a delete, whose last step truncates the file.
Vacuum(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, "v.db")
    defer db.Close()
    try db.Exec(sys.io, "create table t(id integer primary key, v text not null)")
    try Fill(&db, sys, 20000)
    try db.Exec(sys.io, "delete from t where id > 10")
    free := try One(&db, sys, "pragma freelist_count")
    try db.Exec(sys.io, "vacuum")
    after := try One(&db, sys, "pragma freelist_count")
    n := try One(&db, sys, "select count(*) from t")
    try Print(out, "vacuum free {f} -> {a}, rows {n}\n", (f: free > 0, a: after, n: n))
    try db.Exec(sys.io, "insert into t(v) values('after')")
    try Text(&db, sys, out, "integrity", "pragma integrity_check")
}

// TRUNCATE journal mode: each commit truncates the journal to zero, and the
// next transaction creates it again over the empty file.
TruncateMode(sys: &System, out: mut Writer, path: str): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, path)
    defer db.Close()
    try db.Exec(sys.io, "pragma journal_mode = truncate; create table t(id integer primary key, v text not null)")
    try db.Exec(sys.io, "insert into t(v) values('a'); insert into t(v) values('b'); begin; insert into t(v) values('c'); rollback")
    n := try One(&db, sys, "select count(*) from t")
    try Print(out, "truncate mode {n}\n", (n: n))
}

// SQL-6: two spellings of one file are one lock record.
Spellings(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    a := try sqlite.Open(sys.arena, sys.io, "s.db")
    defer a.Close()
    try a.Exec(sys.io, "create table t(id integer primary key, v text not null); insert into t(v) values('x')")
    b := try sqlite.Open(sys.arena, sys.io, "./s.db")
    defer b.Close()
    c := try sqlite.Open(sys.arena, sys.io, ".//s.db")
    defer c.Close()
    try a.Exec(sys.io, "begin exclusive; insert into t(v) values('y')")
    match One(&b, sys, "select count(*) from t") {
        ok(n) {
            try Print(out, "./s.db during {n}\n", (n: n))
        }
        err(e) {
            try Show(out, "./s.db during", e)
        }
    }
    match One(&c, sys, "select count(*) from t") {
        ok(n) {
            try Print(out, ".//s.db during {n}\n", (n: n))
        }
        err(e) {
            try Show(out, ".//s.db during", e)
        }
    }
    try a.Exec(sys.io, "commit")
    n := try One(&b, sys, "select count(*) from t")
    try Print(out, "./s.db after {n}\n", (n: n))
}

// SQL-7: a path with a NUL byte names no file.
OpenNul(sys: &System): sqlite.Error!void {
    d := try sqlite.Open(sys.arena, sys.io, "n1.db\u{0}tail")
    d.Close()
}

// SQL-8: dropping a table while one of its statements is stepping is
// SQLITE_LOCKED, which waiting cannot clear: Sqlite, not Busy.
Locked(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    try db.Exec(sys.io, "create table t(i integer); insert into t values(1), (2)")
    var st := try db.Prepare(sys.arena, sys.io, "select i from t")
    defer st.Close()
    _ = try st.Step(sys.io)
    db.Exec(sys.io, "drop table t") else e {
        try Show(out, "drop while stepping", e)
    }
}

// SQL-15: the documented rules no test held.
Rules(sys: &System, out: mut Writer): sqlite.Error|WriteError!void {
    db := try sqlite.Open(sys.arena, sys.io, ":memory:")
    defer db.Close()
    // Text that is not UTF-8 reads as "" through Str; its bytes are there.
    var st := try db.Prepare(sys.arena, sys.io, "select cast(x'ff41c3' as text), length(cast(x'ff41c3' as blob)), cast(x'41c3a9' as text)")
    defer st.Close()
    _ = try st.Step(sys.io)
    try Print(out, "not utf-8 [{s}] {n} [{t}]\n", (s: st.Str(0), n: st.Int(1), t: st.Str(2)))
    // SQLITE_TOOBIG past SQLITE_MAX_LENGTH (10^9 bytes).
    db.Exec(sys.io, "select zeroblob(1000000001)") else e {
        try Show(out, "too big", e)
    }
    // Canceled in a cancelled scope, before the lock; NoCancel runs.
    var cancelled := 0
    var shielded := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        db.Exec(sys.io, "select 1") else e {
            match e {
                Canceled {
                    cancelled = 1
                }
                _ {
                    cancelled = 2
                }
            }
        }
        _ = One(&db, sys, "select 1") else {
            cancelled += 10
        }
        db.Exec(sys.io.NoCancel(), "select 1") else {
            shielded = 1
        }
    }
    try Print(out, "cancelled scope {c} {s}\n", (c: cancelled, s: shielded))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    RollbackBig(sys, mut out) else e {
        try Show(mut out, "rollback big", e)
    }
    Reopen(sys, mut out, "r.db") else e {
        try Show(mut out, "reopen r.db", e)
    }
    RollbackFirst(sys, mut out) else e {
        try Show(mut out, "rollback first", e)
    }
    Vacuum(sys, mut out) else e {
        try Show(mut out, "vacuum", e)
    }
    Reopen(sys, mut out, "v.db") else e {
        try Show(mut out, "reopen v.db", e)
    }
    TruncateMode(sys, mut out, "j.db") else e {
        try Show(mut out, "truncate mode", e)
    }
    Reopen(sys, mut out, "j.db") else e {
        try Show(mut out, "reopen j.db", e)
    }
    // k.db is never reopened, so its TRUNCATE-mode journal stays, empty.
    TruncateMode(sys, mut out, "k.db") else e {
        try Show(mut out, "truncate mode k.db", e)
    }
    Spellings(sys, mut out) else e {
        try Show(mut out, "spellings", e)
    }
    OpenNul(sys) else e {
        try Show(mut out, "nul in path", e)
    }
    Locked(sys, mut out) else e {
        try Show(mut out, "locked", e)
    }
    Rules(sys, mut out) else e {
        try Show(mut out, "rules", e)
    }
}
`

// findingsWant is findingsProgram's output. No "write after reopen" line:
// an existing file opens read-write, so the write
// after each reopen succeeds.
const findingsWant = `rollback 100010 -> 10
integrity ok
reopened r.db 5011
integrity ok
first rollback tables 0
vacuum free true -> 0, rows 10
integrity ok
reopened v.db 11
integrity ok
truncate mode 2
reopened j.db 2
integrity ok
truncate mode 2
./s.db during: busy
.//s.db during: busy
./s.db after 2
nul in path: sqlite 14
drop while stepping: sqlite 6
not utf-8 [] 3 [Aé]
too big: sqlite 18
cancelled scope 11 0
`

// checkFindingsFiles checks what the program leaves in dir: no journal of
// a finished transaction, the database whose first transaction rolled back
// emptied to zero bytes, no file named by the text before a NUL, and k.db's
// TRUNCATE-mode journal at zero bytes.
// j.db's journal is gone: the reopen after its TruncateMode writes (an
// existing file opens read-write) in the default DELETE mode, since
// a journal mode other than WAL is the connection's, not the file's.
func checkFindingsFiles(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"r.db-journal", "v.db-journal", "z.db-journal", "s.db-journal", "j.db-journal", "n1.db"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s exists after the run", f)
		}
	}
	for _, f := range []string{"z.db", "k.db-journal"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Size() != 0 {
			t.Errorf("%s: want an empty file, got %v %v", f, st, err)
		}
	}
}

// TestStdSqlite_Findings runs findingsProgram at dev and release, each in
// a fresh directory, and checks the files it leaves.
func TestStdSqlite_Findings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		r := newSqliteRoot(t, findingsProgram, true, "[]")
		if got := r.run(t, flags...); got != findingsWant {
			t.Errorf("findings %v:\ngot:\n%s\nwant:\n%s", flags, got, findingsWant)
		}
		checkFindingsFiles(t, r.pkg)
	}
}

// TestStdSqlite_FindingsBuildLinux builds findingsProgram for
// x86_64-linux at dev and release; it starts no wsl.exe.
func TestStdSqlite_FindingsBuildLinux(t *testing.T) {
	for _, flags := range [][]string{nil, {"--release"}} {
		buildSqliteLinux(t, findingsProgram, flags...)
	}
}

// TestStdSqlite_FindingsLinux runs findingsProgram under WSL at release
// with the Windows output, and then once more under strace when WSL has
// it, to check a directory fsync after a journal is created and
// after one is deleted (os_unix.c's UNIXFILE_DIRSYNC and unixDelete).
func TestStdSqlite_FindingsLinux(t *testing.T) {
	needWSL(t)
	exe := buildSqliteLinux(t, findingsProgram, "--release")
	out, stderr, code := runWSL(t, exe, nil)
	if code != 0 || out != findingsWant {
		t.Fatalf("linux: exit %d\nstdout:\n%s\nstderr:\n%s\nwant:\n%s", code, out, stderr, findingsWant)
	}
	checkFindingsFiles(t, filepath.Dir(exe))
	dir := filepath.Dir(exe)
	for _, f := range []string{"r.db", "z.db", "v.db", "j.db", "j.db-journal", "k.db", "k.db-journal", "s.db"} {
		os.Remove(filepath.Join(dir, f))
	}
	_, _, has := runWSL(t, exe, []string{"sh", "-c", "command -v strace"})
	if has != 0 {
		t.Log("strace is not installed in WSL: SQL-9's directory syncs are not checked here")
		return
	}
	out, stderr, code = runWSL(t, exe, []string{"strace", "-f", "-qq", "-o", "trace.txt", "-e", "trace=openat,fsync,fdatasync,unlinkat,unlink,close"})
	if code != 0 || out != findingsWant {
		t.Fatalf("linux under strace: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, stderr)
	}
	trace, err := os.ReadFile(filepath.Join(dir, "trace.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := dirSyncs(string(trace)); n < 2 {
		t.Errorf("SQL-9: %d fsyncs of the database directory, want one after a journal is created and one after it is deleted\n%s", n, trace)
	}
}

// dirSyncs counts the fsyncs of a descriptor that an openat of "." (the
// databases' directory, as SyncParent opens it) returned.
func dirSyncs(trace string) int {
	open := regexp.MustCompile(`openat\(AT_FDCWD, "\.", [^)]*\) = (\d+)`)
	fds := map[string]bool{}
	n := 0
	for _, line := range strings.Split(trace, "\n") {
		if m := open.FindStringSubmatch(line); m != nil {
			fds[m[1]] = true
			continue
		}
		for fd := range fds {
			if strings.Contains(line, "fsync("+fd+")") && strings.Contains(line, "= 0") {
				n++
			}
			if strings.Contains(line, "close("+fd+")") {
				delete(fds, fd)
			}
		}
	}
	return n
}

// contentionProgram: 64 tasks park on the lock in Stmt.Int (a method
// without an Io) while one task runs a statement of about a second; a
// task parked in Exec is cancelled while the statement still runs; a
// ticker that sleeps 1 ms at a time must keep running throughout.
const contentionProgram = `import std.atomic
import std.db.sqlite
import std.time

// Long runs one statement of a second or so, holding the lock throughout.
Long(db: &sqlite.Db, io: Io, started: &atomic.AtomicInt, done: &atomic.AtomicInt) {
    started.Store(1)
    db.Exec(io, "with recursive c(x) as (select 1 union all select x + 1 from c where x < 10000000) select count(*) from c") else {
        done.Store(-1)
        return
    }
    done.Store(1)
}

// Waiter prepares and steps its statement, reports ready, waits for the
// start, then reads the column: a method without an Io, which waits for
// the lock through the Io the statement was prepared with.
Waiter(db: &sqlite.Db, heap: &Heap, io: Io, ready: &atomic.AtomicInt, start: &atomic.AtomicInt, got: &atomic.AtomicInt) {
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var st := db.Prepare(&arena, io, "select 7") else {
        return
    }
    defer st.Close()
    _ = st.Step(io) else {
        return
    }
    _ = ready.Add(1)
    for start.Load() == 0 {
        io.Sleep(time.Millis(1)) else {}
    }
    _ = got.Add(st.Int(0))
}

// Ticker is a task that needs a carrier now and then while Long runs.
Ticker(io: Io, done: &atomic.AtomicInt, ticks: &atomic.AtomicInt) {
    for done.Load() == 0 {
        io.Sleep(time.Millis(1)) else {
            return
        }
        _ = ticks.Add(1)
    }
}

// Parked waits for the lock on an Exec; its scope is cancelled while Long
// still runs: 1 is Canceled while Long ran, 2 Canceled after, 3 another
// error, 4 no error.
Parked(db: &sqlite.Db, io: Io, res: &atomic.AtomicInt, done: &atomic.AtomicInt) {
    db.Exec(io, "select 1") else e {
        match e {
            Canceled {
                if done.Load() == 0 {
                    res.Store(1)
                } else {
                    res.Store(2)
                }
            }
            _ {
                res.Store(3)
            }
        }
        return
    }
    res.Store(4)
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    db := sqlite.Open(sys.arena, sys.io, ":memory:") else {
        try Print(mut out, "open failed\n")
        return
    }
    defer db.Close()
    ready := atomic.AtomicInt.New(0)
    start := atomic.AtomicInt.New(0)
    started := atomic.AtomicInt.New(0)
    done := atomic.AtomicInt.New(0)
    got := atomic.AtomicInt.New(0)
    ticks := atomic.AtomicInt.New(0)
    res := atomic.AtomicInt.New(0)
    var during: i64 = 0
    scope s := sys.io.Scope() {
        for _ in 0..64 {
            s.go Waiter(&db, sys.heap, sys.io, &ready, &start, &got)
        }
        for ready.Load() < 64 {
            sys.io.Sleep(time.Millis(1)) else {}
        }
        s.go Long(&db, sys.io, &started, &done)
        for started.Load() == 0 {
            sys.io.Sleep(time.Millis(1)) else {}
        }
        sys.io.Sleep(time.Millis(20)) else {}
        start.Store(1)
        s.go Ticker(sys.io, &done, &ticks)
        scope c := sys.io.Scope() {
            c.go Parked(&db, sys.io, &res, &done)
            sys.io.Sleep(time.Millis(20)) else {}
            c.Cancel()
        }
        during = done.Load()
    }
    try Print(mut out, "long {d}, cancel while held {c} (long done then {w}), reads {g}\n", (d: done.Load(), c: res.Load(), w: during, g: got.Load()))
    try Print(mut out, "ticks during long {t}\n", (t: ticks.Load()))
}
`

var contentionLine = regexp.MustCompile(`^long 1, cancel while held 1 \(long done then 0\), reads 448\nticks during long (\d+)\n$`)

// TestStdSqlite_Contention runs contentionProgram at release. The
// waiters must all read their row (no lost wakeup), the cancelled waiter
// must leave with Canceled while the long statement holds the lock, and
// the ticker must run during it. Under MK_TIMING=1 (tools/gate, nothing
// else running) it also wants the process's CPU time under 1.5 times its
// wall time plus 0.25 s: 64 parked waiters cost no CPU, where a spinning
// lock would keep every carrier busy for the whole statement.
func TestStdSqlite_Contention(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	r := newSqliteRoot(t, contentionProgram, true, "[]")
	if out, st := r.mk(t, "build", "--target=x86_64-windows", "--release", "."); st != 0 {
		t.Fatalf("mk build: exit %d\n%s", st, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(r.pkg, "p.exe"))
	cmd.Dir = r.pkg
	var o bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &o
	begin := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("p.exe: %v\n%s", err, o.String())
	}
	wall := time.Since(begin)
	got := strings.ReplaceAll(o.String(), "\r\n", "\n")
	m := contentionLine.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("contention:\n%s", got)
	}
	ticks, _ := strconv.Atoi(m[1])
	cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	t.Logf("ticks %d, cpu %v, wall %v", ticks, cpu, wall)
	if ticks < 10 {
		t.Errorf("the ticker ran %d times during the long statement: a carrier was held by a waiter", ticks)
	}
	if os.Getenv("MK_TIMING") == "1" && cpu > wall*3/2+250*time.Millisecond {
		t.Errorf("cpu %v over a wall of %v: the waiters spin instead of parking", cpu, wall)
	}
}

// nomemProgram sets a heap limit and asks for more (NOMEM is
// OutOfMemory). The heap limits work only with memory statistics on, so
// its package builds the amalgamation with SQLITE_DEFAULT_MEMSTATUS=1.
const nomemProgram = `import std.db.sqlite

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    db := sqlite.Open(sys.arena, sys.io, ":memory:") else {
        try Print(mut out, "open failed\n")
        return
    }
    defer db.Close()
    db.Exec(sys.io, "pragma hard_heap_limit = 4000000") else {
        try Print(mut out, "pragma failed\n")
    }
    db.Exec(sys.io, "select length(randomblob(10000000))") else e {
        match e {
            OutOfMemory {
                try Print(mut out, "out of memory\n")
            }
            _ {
                try Print(mut out, "other error\n")
            }
        }
        return
    }
    try Print(mut out, "no error\n")
}
`

// TestStdSqlite_NoMem runs nomemProgram at dev.
func TestStdSqlite_NoMem(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the program is built for x86_64-windows")
	}
	r := newSqliteRoot(t, nomemProgram, true, "[]")
	man := strings.Replace(sqlitePkg(true, "[]"), "-DSQLITE_DEFAULT_MEMSTATUS=0", "-DSQLITE_DEFAULT_MEMSTATUS=1", 1)
	if err := os.WriteFile(filepath.Join(r.pkg, "pkg.mk"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.run(t); got != "out of memory\n" {
		t.Errorf("nomem: %q", got)
	}
}
