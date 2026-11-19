// Why: std.fs's operations must be cancel-safe as documented: repeatable or
// prefix when a scope's deadline cuts them.
// What: TestFs_SimCancel, which runs the std.fsprobe blocks under std.sim,
// dev and release.
// How: each operation is cut at successive points of the Sim's clock and
// called again; the blocks sit in a probe module of a copied std.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fs2SimBlocks = `// The std.sim cancel-safety tests of std.fs's 1.2 operations (the
// cancel-safety table of doc/std/core.md), in a probe module of a copied std:
// a test block of std.fs cannot import std.sim, whose own tests import
// std.fs (E0205). Each body runs on a task of a Sim, cut by a scope's
// deadline at successive points of the Sim's clock and called again.

import std.atomic
import std.fs
import std.sim
import std.time

// Sims is the seeds each block runs.
const Sims = 6

// Name is e as it prints.
Name(a: &Arena, e: fs.Error | OutOfMemory): str from a {
    return a.Fmt("{e}", (e)).Must()
}

// Tree makes under root, on io, n directories d<i>, each with a nested
// directory deep holding one file, and k files.
Tree(a: &Arena, io: Io, root: str, n: int, k: int) {
    for i in 0..n {
        d := a.Fmt("{root}/d{i}", (root, i)).Must()
        fs.MakeDirAll(io, a.Fmt("{d}/deep", (d)).Must()).Must()
        fs.WriteAll(io, a.Fmt("{d}/deep/x", (d)).Must(), "xyz".AsBytes()).Must()
        for j in 0..k {
            fs.WriteAll(io, a.Fmt("{d}/f{j}", (d, j)).Must(), "f".AsBytes()).Must()
        }
    }
}

// Next appends w's next entry to t as "depth kind path": 1, or 2 at the
// end.
Next(w: mut fs.Walker, a: &Arena, io: Io, t: mut Text): fs.Error|OutOfMemory!int {
    e := try w.Next(a, io) else {
        return 2
    }
    k := if e.kind == fs.Kind.Dir { "d" } else { "f" }
    var out := t.Writer()
    Print(mut out, "{depth} {k} {p}\n", (depth: e.depth, k: k, p: &e.path)) else {
        fail OutOfMemory
    }
    return 1
}

// Step is Next in a scope whose deadline is d microseconds away (none when
// d < 0): 0 when the deadline cut it with Canceled.
Step(w: mut fs.Walker, a: &Arena, io: Io, t: mut Text, d: i64): int {
    var r := 0
    scope s := io.Scope() {
        if d >= 0 {
            s.CancelAfter(time.Micros(d))
        }
        got := Next(w, a, io, t) else e {
            Assert(Name(a, e) == "Canceled")
            0
        }
        r = got
    }
    return r
}

// Entries is the number of entries fs.Walk lists under root, root
// included, in the loop form; 0 when root is missing.
Entries(h: &Heap, a: &Arena, io: Io, root: str): fs.Error|OutOfMemory!int {
    _ = fs.StatLink(io, root) else {
        return 0
    }
    var w := try fs.Walk(h, io, root)
    defer w.Close()
    var n := 0
    for e := try w.Next(a, io) {
        _ = e
        n += 1
    }
    return n
}

// LockTask: a deadline ends a waiting File.Lock with Canceled and nothing
// taken; the repeated call takes the lock. 1 in out when it ran through.
LockTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    fs.WriteAll(io, "l", "x".AsBytes()).Must()
    f := fs.OpenWith(io, "l", fs.ReadWrite).Must()
    g := fs.OpenWith(io, "l", fs.ReadWrite).Must()
    f.Lock(io, fs.LockMode.Exclusive).Must()
    var got := "none"
    t0 := time.Now(io)
    scope d := io.Scope() {
        d.CancelAfter(time.Millis(1))
        g.Lock(io, fs.LockMode.Exclusive) else e {
            got = Name(&a, e)
        }
    }
    Assert(got == "Canceled")
    Assert(time.Now(io).Since(t0).Nanos() >= 1_000_000)
    // Nothing taken: f still holds it, and g's try fails.
    Assert(!g.TryLock(io, fs.LockMode.Shared).Must())
    f.Unlock(io).Must()
    g.Lock(io, fs.LockMode.Exclusive).Must()
    Assert(!f.TryLock(io, fs.LockMode.Shared).Must())
    g.Close(io).Must()
    f.Close(io).Must()
    fs.Remove(io, "l").Must()
    out.Store(1)
}

// WalkTask: each step is tried with deadlines 0, 1, 2, 4, ... microseconds
// away until one lets it finish, so the cuts land at each await point a
// step has; the listing equals an uncut walk's. The cuts in out.
WalkTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    Tree(&a, io, "t", 3, 3)
    var want := Text.New(&a)
    var w := fs.Walk(h, io, "t").Must()
    for Step(mut w, &a, io, mut want, -1) == 1 {
    }
    w.Close()
    var got := Text.New(&a)
    var v := fs.Walk(h, io, "t").Must()
    var cuts := 0
    var d: i64 = 0
    for {
        r := Step(mut v, &a, io, mut got, d)
        if r == 2 {
            break
        }
        if r == 0 {
            cuts += 1
            d = if d == 0 { 1 } else { d * 2 }
        } else {
            d = 0
        }
    }
    v.Close()
    Assert(want.Str().Len() > 0)
    Assert(got.Str() == want.Str())
    fs.RemoveAll(io, "t").Must()
    out.Store(i64(cuts))
}

// PrefixTask: RemoveAll and MakeDirAll under deadlines 0, 1, 2, 4, ...
// microseconds away until one lets the call finish uncut; after each cut
// what is left is part of the tree (what was made, a prefix of the path),
// and a second call finishes. The cuts that landed inside a tree in out.
PrefixTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    var partial := 0
    Tree(&a, io, "t", 4, 2)
    full := Entries(h, &a, io, "t").Must()
    var d: i64 = 0
    for {
        var got := "none"
        scope c := io.Scope() {
            c.CancelAfter(time.Micros(d))
            fs.RemoveAll(io, "t") else e {
                got = Name(&a, e)
            }
        }
        left := Entries(h, &a, io, "t").Must()
        if got == "none" {
            Assert(left == 0)
            break
        }
        Assert(got == "Canceled")
        Assert(left <= full)
        if left > 0 && left < full {
            partial += 1
        }
        fs.RemoveAll(io, "t").Must()
        Assert(Entries(h, &a, io, "t").Must() == 0)
        Tree(&a, io, "t", 4, 2)
        d = if d == 0 { 1 } else { d * 2 }
    }
    deep := "m/a/b/c/d/e/f/g/h"
    d = 0
    for {
        var got := "none"
        scope c := io.Scope() {
            c.CancelAfter(time.Micros(d))
            fs.MakeDirAll(io, deep) else e {
                got = Name(&a, e)
            }
        }
        made := Entries(h, &a, io, "m").Must()
        if got == "none" {
            Assert(made == 9)
            break
        }
        Assert(got == "Canceled")
        Assert(made <= 9)
        if made > 0 && made < 9 {
            partial += 1
        }
        fs.MakeDirAll(io, deep).Must()
        Assert(Entries(h, &a, io, "m").Must() == 9)
        fs.RemoveAll(io, "m").Must()
        d = if d == 0 { 1 } else { d * 2 }
    }
    fs.RemoveAll(io, "m").Must()
    out.Store(i64(partial))
}

// CopyTask: fs.Copy under deadlines 0, 1, 2, 4, ... microseconds away
// until one lets it finish uncut; after each cut dst is absent or a prefix
// of src, and a second Copy copies every byte. The cuts that left a
// non-empty prefix in out.
CopyTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    var t := Text.New(&a)
    for i in 0..5000 {
        t.Push(if i % 7 == 0 { "0123456\n" } else { "abcdefg" }).Must()
    }
    src := t.Str()
    fs.WriteAll(io, "s", src.AsBytes()).Must()
    var partial := 0
    var d: i64 = 0
    for {
        _ = fs.Remove(io, "c") else {
        }
        var got := "none"
        scope c := io.Scope() {
            c.CancelAfter(time.Micros(d))
            _ = fs.Copy(io, "s", "c") else e {
                got = Name(&a, e)
            }
        }
        if got == "none" {
            Assert(fs.ReadText(&a, io, "c").Must() == src)
            break
        }
        Assert(got == "Canceled")
        left := fs.ReadText(&a, io, "c") else {
            ""
        }
        Assert(src.StartsWith(left))
        if left.Len() > 0 && left.Len() < src.Len() {
            partial += 1
        }
        Assert(fs.Copy(io, "s", "c").Must() == i64(src.Len()))
        Assert(fs.ReadText(&a, io, "c").Must() == src)
        d = if d == 0 { 1 } else { d * 2 }
    }
    // A copy onto itself is Denied and keeps the bytes.
    var self := "none"
    _ = fs.Copy(io, "s", "s") else e {
        self = Name(&a, e)
    }
    Assert(self == "Denied" && fs.ReadText(&a, io, "s").Must() == src)
    fs.Remove(io, "s").Must()
    fs.Remove(io, "c").Must()
    out.Store(i64(partial))
}

// Remover runs RemoveAll(root), 1 in out when it succeeded.
Remover(io: Io, root: str, out: &atomic.AtomicInt) {
    fs.RemoveAll(io, root) else {
        out.Store(2)
        return
    }
    out.Store(1)
}

// TwoTask: two removers of one tree at once, 8 rounds: both succeed and
// the tree is gone. The rounds in out.
TwoTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    for round in 0..8 {
        _ = round
        Tree(&a, io, "t", 20, 4)
        x := atomic.AtomicInt.New(0)
        y := atomic.AtomicInt.New(0)
        scope s := io.Scope() {
            s.go Remover(io, "t", &x)
            s.go Remover(io, "t", &y)
        }
        Assert(x.Load() == 1 && y.Load() == 1)
        Assert(Entries(h, &a, io, "t").Must() == 0)
    }
    out.Store(8)
}

// Creator writes files into dir until stop is set or a write fails;
// nested, it makes dir/a/b/c/d/e again each time first, so every level of
// a chain refills.
Creator(io: Io, h: &Heap, dir: str, nested: bool, stop: &atomic.AtomicBool) {
    var a := Arena.New(h).Must()
    defer a.Free()
    var i := 0
    for !stop.Load() {
        at := if nested { a.Fmt("{dir}/a/b/c/d/e", (dir)).Must() } else { dir }
        if nested {
            fs.MakeDirAll(io, at) else {
                break
            }
        }
        fs.WriteAll(io, a.Fmt("{at}/c{i}", (at, i)).Must(), "c".AsBytes()) else {
            if nested {
                continue
            }
            break
        }
        i += 1
        if i % 64 == 0 {
            a.Reset()
        }
    }
}

// FillTask: RemoveAll of a tree while a creator fills a directory of it,
// or refills a chain of five levels, returns, Ok or the not-empty code,
// never cut by the 10 s deadline. 2 in out.
FillTask(io: Io, h: &Heap, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    for nested in [false, true] {
        fs.MakeDirAll(io, "r/sub").Must()
        stop := atomic.AtomicBool.New(false)
        var got := "ok"
        scope s := io.Scope() {
            s.go Creator(io, h, "r/sub", nested, &stop)
            scope d := io.Scope() {
                d.CancelAfter(time.Seconds(10))
                fs.RemoveAll(io, "r") else e {
                    got = Name(&a, e)
                }
            }
            stop.Store(true)
        }
        Assert(got == "ok" || got.StartsWith("Os("))
        fs.RemoveAll(io, "r").Must()
    }
    out.Store(2)
}

// Run runs the task k (0 lock, 1 walk, 2 prefix, 3 copy, 4 two removers,
// 5 a creator) on a Sim of each seed and returns the sum of what each
// stored.
Run(sys: &System, k: int): int {
    var sum := 0
    for seed in 0..Sims {
        var s := sim.Sim.New(sys.arena, Cast[u64](seed)).Must()
        defer s.Free()
        io := s.Io()
        out := atomic.AtomicInt.New(-1)
        scope r := io.Scope() {
            if k == 0 {
                r.go LockTask(io, sys.heap, &out)
            } else if k == 1 {
                r.go WalkTask(io, sys.heap, &out)
            } else if k == 2 {
                r.go PrefixTask(io, sys.heap, &out)
            } else if k == 3 {
                r.go CopyTask(io, sys.heap, &out)
            } else if k == 4 {
                r.go TwoTask(io, sys.heap, &out)
            } else {
                r.go FillTask(io, sys.heap, &out)
            }
        }
        Assert(out.Load() >= 0)
        sum += Trunc[int](out.Load())
    }
    return sum
}

test "under std.sim: File.Lock cut by a deadline takes nothing, and called again takes the lock" (sys: &System) {
    Assert(Run(sys, 0) == Sims)
}

test "under std.sim: Walker.Next cut at successive points and called again lists what an uncut walk lists" (sys: &System) {
    // Each step of a 19-entry walk is cut at least once (deadline 0).
    Assert(Run(sys, 1) >= Sims * 19)
}

test "under std.sim: RemoveAll and MakeDirAll cut by a deadline keep what they did, and a second call finishes" (sys: &System) {
    // Some cut landed inside a tree, not only before or after it.
    Assert(Run(sys, 2) > 0)
}

test "under std.sim: Copy cut by a deadline leaves dst absent or a prefix of src, and a second call copies it all" (sys: &System) {
    // Some cut left a part of src, not only nothing or everything.
    Assert(Run(sys, 3) > 0)
}

test "RemoveAll under std.sim: two removers of one tree at once both succeed" (sys: &System) {
    Assert(Run(sys, 4) == Sims * 8)
}

test "RemoveAll under std.sim: a creator that never stops filling a directory, or refilling a chain of them, does not keep it running" (sys: &System) {
    Assert(Run(sys, 5) == Sims * 2)
}
`

// TestFs_SimCancel runs the std.fsprobe blocks, dev and release, on
// Windows; the Sim is the same on both targets (its disk and its clock
// are its own).
//
// Under a Sim's Io each operation is cut by a scope's deadline at successive
// points of the Sim's clock and called again. File.Lock and Walker.Next are
// repeatable: what the calls deliver after a cut equals an uncut run.
// fs.MakeDirAll, fs.RemoveAll and fs.Copy are prefix: what they did before
// the cut stays done, and a second call finishes (a Sim answers the copy call
// Unsupported, so fs.Copy runs its read and write loop there). Two more
// blocks hold RemoveAll to what a race may do: two removers of one tree both
// succeed, and a creator that never stops filling a directory does not keep
// it running. The blocks live in a probe module, std.fsprobe, because a test
// block of std.fs cannot import std.sim, whose own tests import std.fs
// (E0205). Each body runs on a task of the Sim: on the main task a scope's
// cancel did not cut these calls.
func TestFs_SimCancel(t *testing.T) {
	root := t.TempDir()
	stdDir := fs2Std(t, root)
	if err := os.MkdirAll(filepath.Join(stdDir, "fsprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stdDir, "fsprobe", "tests_fs2sim.mk"), []byte(fs2SimBlocks), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, flags := range [][]string{nil, {"--release"}} {
		args := append([]string{"test", "--std=std.fsprobe"}, flags...)
		out, status := mkCmd(t, root, stdDir, args...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		if status != 0 || !strings.Contains(out, "6 passed, 0 failed") {
			t.Fatalf("mk %s: status %d\n%s", strings.Join(args, " "), status, out)
		}
	}
}
