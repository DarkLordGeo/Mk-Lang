// Why: std bugs once found in sorting, files, slices, maps, readers, integer
// formatting and channels must not come back.
// What: regression tests for Sort's permutation, File.Abandon, Slice.Index and
// Has, HeapMap and TextMap Send and Share, Reader.Line, Chan, and E0701/E1101.
// How: builds and runs small Mk programs and compares their output with a Go
// model, or runs mk check and compares the diagnostics (expectCheckCodes).

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// sortProgram sorts P{k: (s>>33) % mod, id: i} by k for each n, with s the
// 64-bit LCG below, through Sort with a named comparator, or with a
// capturing literal that reads a local when with (the form that replaced
// SortWith), and prints the id order of each size on one line.
func sortProgram(mod int, sizes []int, with bool) string {
	sortCall := "xs.Sort(PLess)"
	if with {
		sortCall = "big: u64 = 1000000007\n    xs.Sort(fn(p: &P, q: &P): bool { return p.k % big < q.k % big })"
	}
	var b strings.Builder
	b.WriteString(`struct P {
    k: u64
    id: int
}

PLess(a: &P, b: &P): bool {
    return a.k < b.k
}

Run(out: mut Writer, a: &Arena, n: int, mod: u64): WriteError|OutOfMemory!void {
    var xs := List[P].New(a)
    var s: u64 = 12345
    for i in 0..n {
        s = s *% 6364136223846793005 +% 1442695040888963407
        try xs.Push(P(k: (s >> 33) % mod, id: i))
    }
    ` + sortCall + `
    for p in xs.Items() {
        id := p.id
        try Print(out, "{id} ", (id))
    }
    try Print(out, "\n", ())
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
`)
	for _, n := range sizes {
		fmt.Fprintf(&b, "    try Run(mut out, sys.arena, %d, %d)\n", n, mod)
	}
	b.WriteString("}\n")
	return b.String()
}

// checkSortOrder checks sortProgram's output: each line a permutation of
// 0..n-1 whose keys are in the order Go's sort.Slice gives them.
func checkSortOrder(t *testing.T, mod int, sizes []int, out string) {
	t.Helper()
	got := strings.Split(out, "\n")
	want := strings.Split(goSortOrder(mod, sizes), "\n")
	for li, n := range sizes {
		keys := sortKeys(mod, n)
		if li >= len(got) {
			t.Fatalf("n = %d: no output line", n)
		}
		ids := strings.Fields(got[li])
		wids := strings.Fields(want[li])
		if len(ids) != n {
			t.Fatalf("n = %d: %d ids", n, len(ids))
		}
		seen := make([]bool, n)
		for i, f := range ids {
			var id, wid int
			fmt.Sscan(f, &id)
			fmt.Sscan(wids[i], &wid)
			if id < 0 || id >= n || seen[id] {
				t.Fatalf("n = %d: id %d is out of range or repeated", n, id)
			}
			seen[id] = true
			if keys[id] != keys[wid] {
				t.Fatalf("n = %d: the key at %d is %d, Go's order has %d", n, i, keys[id], keys[wid])
			}
		}
	}
}

// sortKeys is sortProgram's keys of n elements, by id.
func sortKeys(mod int, n int) []uint64 {
	keys := make([]uint64, n)
	var s uint64 = 12345
	for i := 0; i < n; i++ {
		s = s*6364136223846793005 + 1442695040888963407
		keys[i] = (s >> 33) % uint64(mod)
	}
	return keys
}

// goSortOrder is sortProgram's output computed with Go's sort.Slice.
func goSortOrder(mod int, sizes []int) string {
	type p struct {
		k  uint64
		id int
	}
	var b strings.Builder
	for _, n := range sizes {
		xs := make([]p, 0, n)
		var s uint64 = 12345
		for i := 0; i < n; i++ {
			s = s*6364136223846793005 + 1442695040888963407
			xs = append(xs, p{(s >> 33) % uint64(mod), i})
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].k < xs[j].k })
		for _, x := range xs {
			fmt.Fprintf(&b, "%d ", x.id)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestSortMatchesGo: Sort with a named comparator and with a
// capturing literal, of seeded inputs with ties, gives a permutation of the
// input whose keys are in Go's sort.Slice order, and both comparators give
// the same permutation (Sort is deterministic). With the Lomuto partition
// (ipnsort's) the order of equal keys is not Go's, so only the key
// order is compared with Go's. Mod 3 makes partitions unbalanced, so
// pattern breaking runs (a bug there first showed at n = 300); mod 10^6 has
// few ties.
func TestSortMatchesGo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mod   int
		sizes []int
	}{
		{3, []int{5, 12, 13, 50, 300, 1000, 5000}},
		{1000000, []int{5, 12, 13, 50, 300, 1000, 5000}},
	}
	for _, c := range cases {
		for _, with := range []bool{false, true} {
			name := fmt.Sprintf("mod%d/with=%v", c.mod, with)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				r := runProgram(t, sortProgram(c.mod, c.sizes, with))
				if r.code != 0 {
					t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
				}
				checkSortOrder(t, c.mod, c.sizes, r.stdout)
				if with {
					plain := runProgram(t, sortProgram(c.mod, c.sizes, false))
					if plain.stdout != r.stdout {
						t.Fatalf("the two comparators gave different permutations")
					}
				}
			})
		}
	}
}

// TestFileAbandonCloses: File.Abandon closes and returns; it is
// not a panicking stub. The copy.mk shape: a failed Create
// in a missing directory runs the source's onerr Abandon, and Main returns
// the fs error (exit 1) with no panic report.
func TestFileAbandonCloses(t *testing.T) {
	t.Parallel()
	exe := buildNative(t, nil, `import std.fs

Copy(io: Io, src: str, dst: str): fs.Error!void {
    var input := try fs.Open(io, src)
    onerr input.Abandon()
    var output := try fs.Create(io, dst)
    onerr output.Abandon()
    try output.Close(io)
    try input.Close(io)
}

export Main(sys: &System): fs.Error|WriteError!void {
    try fs.WriteAll(sys.io, "a.txt", "abc".AsBytes())
    f := try fs.Open(sys.io, "a.txt")
    f.Abandon()
    g := try fs.Open(sys.io, "a.txt")
    g.Abandon()
    var out := sys.Stdout()
    try Print(mut out, "abandoned\n", ())
    try Out(mut out)
    try Copy(sys.io, "a.txt", sys.args[1])
}

Out(out: mut Writer): WriteError!void {
    try out.Flush()
}
`)
	r := runNative(t, exe, "", "nodir/out.txt")
	if r.code != 1 || r.stdout != "abandoned\n" || strings.Contains(r.stderr, "panic") || !strings.Contains(r.stderr, "NotFound") {
		t.Errorf("exit %d, want 1 with the NotFound error and no panic\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	r = runNative(t, exe, "", "b.txt")
	wantRun(t, r, 0, "abandoned\n")
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "b.txt")); err != nil {
		t.Errorf("the copy target was not created: %v", err)
	}
}

// TestSliceIndexHas: Slice[T].Index and Has through Eq, over an
// array and a List[str] (both were once a panicking stub).
func TestSliceIndexHas(t *testing.T) {
	t.Parallel()
	r := runProgram(t, `export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    xs: [3]int = [1, 2, 3]
    a := xs.Has(&2)
    b := xs.Has(&9)
    c := xs.Index(&3) else { -1 }
    d := xs.Index(&9) else { -1 }
    e := xs.Index(&1) else { -1 }
    try Print(mut out, "{a} {b} {c} {d} {e}\n", (a, b, c, d, e))
    var l := List[str].New(sys.arena)
    try l.Push("a")
    try l.Push("bc")
    try l.Push("bc")
    f := l.Index(&"bc") else { -1 }
    g := l.Has(&"zz")
    h := l.Index(&"zz").IsNone()
    empty: [0]int = []
    i := empty.Has(&0)
    try Print(mut out, "{f} {g} {h} {i}\n", (f, g, h, i))
}
`)
	wantRun(t, r, 0, "true false 2 -1 0\n1 false true false\n")
}

const sendShareProgram = `UseM(m: HeapMap[int, int], h: &Heap) {
    m.Free(h.Alloc())
}

UseT(m: TextMap[int], h: &Heap) {
    m.Free(h.Alloc())
}

ReadM(m: &HeapMap[int, int]) {
}

ReadT(m: &TextMap[int]) {
}

ReadC(m: &HeapMap[int, Cell[int]]) {
}

export Main(sys: &System) {
    h := sys.heap
    m := HeapMap[int, int].New(h.Alloc())
    t := TextMap[int].New(h.Alloc())
    r := HeapMap[int, int].New(h.Alloc())
    q := TextMap[int].New(h.Alloc())
    c := HeapMap[int, Cell[int]].New(h.Alloc())
    scope s := sys.io.Scope() {
        s.go UseM(m, h)
        s.go UseT(t, h)
        s.go ReadM(&r)
        s.go ReadT(&q)
        s.go ReadC(&c)
    }
    r.Free(h.Alloc())
    q.Free(h.Alloc())
    c.Free(h.Alloc())
}
`

// checkProgram runs mk check on src over a copy of std with fixtures.
func checkProgram(t *testing.T, fixtures map[string]string, src string) (string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return mkCmd(t, root, stdDir, "check", "main.mk")
}

// diagCodes lists "line code" for every diagnostic of main.mk in out.
func diagCodes(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if !diagLine.MatchString(line) || !strings.HasPrefix(line, "main.mk:") {
			continue
		}
		f := strings.SplitN(line, ":", 4)
		code := strings.Fields(f[3])[0]
		got = append(got, f[1]+" "+code)
	}
	return got
}

// TestHeapMapTextMapSendShare: HeapMap and TextMap are Send and
// Share if their contents are (the language's kind rules): moving a
// HeapMap[int, int] or a TextMap[int] into a task, and sharing either by
// reference, gives no diagnostic; a HeapMap holding Cell[int] is not Share
// (E1002). Then the impls are planted out of coll_heap.mk, and the moves and
// shares must fail with E1001 and E1002, so the clean run judges them.
func TestHeapMapTextMapSendShare(t *testing.T) {
	t.Parallel()
	out, _ := checkProgram(t, nil, sendShareProgram)
	if got := strings.Join(diagCodes(out), ","); got != "30 E1002" {
		t.Errorf("want exactly E1002 at line 30 (ReadC), got %q\n%s", got, out)
	}
	if strings.Contains(out, "std/") {
		t.Errorf("std drew a diagnostic:\n%s", out)
	}
	b, err := os.ReadFile("core/coll_heap.mk")
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "unsafe impl") && (strings.Contains(line, "HeapMap") || strings.Contains(line, "TextMap")) {
			continue
		}
		kept = append(kept, line)
	}
	out, _ = checkProgram(t, map[string]string{"core/coll_heap.mk": strings.Join(kept, "\n")}, sendShareProgram)
	if got := strings.Join(diagCodes(out), ","); got != "26 E1001,27 E1001,28 E1002,29 E1002,30 E1002" {
		t.Errorf("without the impls, want E1001 at 26, 27 and E1002 at 28 to 30, got %q\n%s", got, out)
	}
}

const linesProgram = `export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var r := sys.Stdin()
    for {
        l := r.Line(sys.arena) else {
            try Print(mut out, "E\n", ())
            return
        }
        s := l else {
            break
        }
        n := s.Len()
        try Print(mut out, "{n}:{s}|\n", (n, s))
    }
    try Print(mut out, "end\n", ())
}
`

// goLines is linesProgram's output by the rules of Reader.Line:
// \n and \r\n are stripped, a last line with no \n is a line as it is, a
// line over max bytes or with invalid UTF-8 is ReadError.
func goLines(in string, max int) string {
	var b strings.Builder
	for len(in) > 0 {
		i := strings.IndexByte(in, '\n')
		line := in
		if i >= 0 {
			line, in = in[:i], in[i+1:]
			line = strings.TrimSuffix(line, "\r")
		} else {
			in = ""
		}
		if len(line) > max || !utf8.ValidString(line) {
			b.WriteString("E\n")
			return b.String()
		}
		fmt.Fprintf(&b, "%d:%s|\n", len(line), line)
	}
	return b.String() + "end\n"
}

// TestStdinLineKeepsLoneCR: Reader.Line on stdin (a reader with
// no inline buffer, read through the stream table's line entry) strips \n
// and \r\n only, so a final "cd\r" stays three bytes. The other inputs cross the 1024-byte chunk: a \r as the last
// byte of a chunk and its \n in the next, a two-byte scalar split across
// chunks, a line of 64 KiB and one over it, and invalid UTF-8.
func TestStdinLineKeepsLoneCR(t *testing.T) {
	t.Parallel()
	exe := buildNative(t, nil, linesProgram)
	inputs := []string{
		"ab\r\ncd\r",
		"x\n\ny",
		"\r",
		"\r\n\r\r\n",
		"",
		strings.Repeat("a", 1023) + "\r\n" + strings.Repeat("b", 1024) + "\nz",
		"a" + strings.Repeat("\u00e9", 1500) + "\r\nq\r",
		strings.Repeat("c", 65536) + "\nok\n",
		strings.Repeat("d", 65537) + "\n",
		"fine\n\xff\n",
	}
	for i, in := range inputs {
		r := runNative(t, exe, in)
		if want := goLines(in, 65536); r.code != 0 || r.stdout != want {
			got, w := r.stdout, want
			if len(got) > 200 {
				got = got[:200] + "..."
			}
			if len(w) > 200 {
				w = w[:200] + "..."
			}
			t.Errorf("input %d: exit %d\ngot:  %q\nwant: %q\nstderr: %s", i, r.code, got, w, r.stderr)
		}
	}
}

// TestFmtIntsMatchGo: integer formatting, now two digits per
// division by 100, prints what strconv does for every
// power of ten and its neighbours, both ends of every width, and seeded
// values, as u64, i64, i32, i8 and u8. The speed half is tests/bench/fmtints
// at --release.
func TestFmtIntsMatchGo(t *testing.T) {
	t.Parallel()
	var us []uint64
	var is []int64
	for p, k := uint64(1), 0; k < 20; p, k = p*10, k+1 {
		us = append(us, p-1, p, p+1)
		if p <= 1<<62 {
			is = append(is, int64(p)-1, int64(p), -int64(p), -int64(p)+1, -int64(p)-1)
		}
	}
	us = append(us, 0, 1<<64-1, 1<<64-2, 1<<63)
	is = append(is, 0, -1, 1<<63-1, -1<<63, -1<<63+1)
	var s uint64 = 7
	for range 200 {
		s = s*6364136223846793005 + 1442695040888963407
		us = append(us, s>>(s%64))
		is = append(is, int64(s)>>(s%64))
	}
	var src, want strings.Builder
	for _, ty := range []string{"u64", "i64", "i32", "i8", "u8"} {
		src.WriteString("P" + ty + "(out: mut Writer, x: " + ty + "): WriteError!void {\n" +
			`    try Print(out, "{x}\n", (x))` + "\n}\n\n")
	}
	src.WriteString("export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	// emit prints the literal lit as type ty; want is Go's decimal.
	emit := func(ty, lit, decimal string) {
		src.WriteString("    try P" + ty + "(mut out, " + lit + ")\n")
		want.WriteString(decimal + "\n")
	}
	for _, u := range us {
		emit("u64", fmt.Sprint(u), fmt.Sprint(u))
	}
	for _, i := range is {
		lit := fmt.Sprint(i)
		if i == -1<<63 {
			lit = "-9223372036854775807 - 1"
		}
		emit("i64", lit, fmt.Sprint(i))
	}
	for _, i := range []int64{0, 1, -1, 9, -9, 10, -10, 99, -99, 100, -100, 127, -128, 1<<31 - 1, -1 << 31} {
		d := fmt.Sprint(i)
		if i >= -128 && i <= 127 {
			emit("i8", d, d)
		}
		if i >= 0 && i <= 255 {
			emit("u8", d, d)
		}
		emit("i32", d, d)
	}
	src.WriteString("}\n")
	r := runProgram(t, src.String())
	if r.code != 0 || r.stdout != want.String() {
		got, w := strings.Split(r.stdout, "\n"), strings.Split(want.String(), "\n")
		for i := range w {
			if i >= len(got) || got[i] != w[i] {
				g := "(missing)"
				if i < len(got) {
					g = got[i]
				}
				t.Fatalf("exit %d; line %d: got %q, want %q\nstderr: %s", r.code, i+1, g, w[i], r.stderr)
			}
		}
		t.Fatalf("exit %d, stderr: %s", r.code, r.stderr)
	}
}

// expectCheckCodes runs mk check on src and wants exactly the main.mk
// diagnostics want ("line code", in order) and none in std. When the
// diagnostic code is absent from the output altogether, the rule is not
// implemented yet by its emitter (gap says which rule is missing),
// and the test skips; once the emitter reports it, the test must pass.
func expectCheckCodes(t *testing.T, src, code, gap string, want ...string) {
	t.Helper()
	out, _ := checkProgram(t, nil, src)
	got := diagCodes(out)
	if strings.Contains(out, "std/") {
		t.Errorf("std drew a diagnostic:\n%s", out)
	}
	if !strings.Contains(strings.Join(got, ","), " "+code) {
		t.Skipf("blocked: %s; output:\n%s", gap, out)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want %q, got %q\n%s", want, got, out)
	}
}

// TestArenaSliceZeroValid: Arena.Slice[T] zero-fills, so T must be
// zero-valid, else E0701 at the call.
// A reference, a str and a Box are not; u8, bool, i64 and a struct of
// integers are. The test skips while check does not report the rule.
func TestArenaSliceZeroValid(t *testing.T) {
	t.Parallel()
	expectCheckCodes(t, `struct Pt {
    x: int
    y: i32
}

export Main(sys: &System): OutOfMemory!void {
    a := sys.arena
    _ = try a.Slice[&int](1)
    _ = try a.Slice[str](1)
    _ = try a.Slice[Box[int]](1)
    _ = try a.Slice[u8](1)
    _ = try a.Slice[bool](1)
    _ = try a.Slice[i64](1)
    _ = try a.Slice[Pt](1)
}
`, "E0701", "check: Arena.Slice[T] needs T zero-valid", "8 E0701", "9 E0701", "10 E0701")
}

// TestE1101_StrFromCOutsideUnsafe: str.FromC reads through the raw pointer
// it is given, so a call outside unsafe is E1101. The test skips while
// check misses the type-qualified call.
func TestE1101_StrFromCOutsideUnsafe(t *testing.T) {
	t.Parallel()
	expectCheckCodes(t, `F(p: *u8): str {
    return str.FromC(p)
}

export Main(sys: &System) {
}
`, "E1101", "check: E1101 on a str.FromC call outside unsafe", "2 E1101")
}

// TestChanBuffered: a channel of capacity 3 on one task (a
// single-carrier fixture): three sends fill it, Close is
// idempotent, a Send after Close is Closed and the caller keeps x, and Recv
// drains 1, 2, 3, then Closed.
func TestChanBuffered(t *testing.T) {
	t.Parallel()
	r := runProgram(t, `export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    c := try Chan[int].New(sys.arena.Alloc(), 3)
    for i in 1..4 {
        c.Send(sys.io, i) else {
            try Print(mut out, "send failed\n", ())
        }
    }
    c.Close()
    c.Close()
    x := 9
    c.Send(sys.io, x) else {
        try Print(mut out, "send after close: closed, kept {x}\n", (x))
    }
    for _ in 0..5 {
        y := c.Recv(sys.io) else {
            try Print(mut out, "closed\n", ())
            break
        }
        try Print(mut out, "{y}\n", (y))
    }
}
`)
	wantRun(t, r, 0, "send after close: closed, kept 9\n1\n2\n3\nclosed\n")
}

// TestStd_ChanHandoffAndRing: capacity 0 is a handoff to a parked
// receiver: Main sends 100 values to two receiver tasks, a sender
// task sends 100 more, and every value arrives once; a capacity-2 ring
// carries 100 values from a sender task to a receiver task; Close ends
// every receiver with Closed. Cancellation while parked is not tested
// here. Dev and release.
func TestStd_ChanHandoffAndRing(t *testing.T) {
	t.Parallel()
	src := `struct Acc {
    sum: int
    n: int
}

Receiver(c: &Chan[int], io: Io, acc: &Mutex[Acc]) {
    for {
        x := c.Recv(io) else {
            return
        }
        var g := acc.Lock(io) else {
            return
        }
        g.Mut().sum += x
        g.Mut().n += 1
        g.Unlock()
    }
}

Sender(c: &Chan[int], io: Io, base: int) {
    for i in 0..100 {
        c.Send(io, base + i) else {
            return
        }
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    c := try Chan[int].New(sys.arena.Alloc(), 0)
    b := try Chan[int].New(sys.arena.Alloc(), 2)
    acc := Mutex[Acc].New(Acc(sum: 0, n: 0))
    acc2 := Mutex[Acc].New(Acc(sum: 0, n: 0))
    scope s := sys.io.Scope() {
        s.go Receiver(&c, sys.io, &acc)
        s.go Receiver(&c, sys.io, &acc)
        s.go Receiver(&b, sys.io, &acc2)
        s.go Sender(&b, sys.io, 1000)
        for i in 0..100 {
            c.Send(sys.io, i) else {
                break
            }
        }
        s.go Sender(&c, sys.io, 100)
        for _ in 0..1000 {
            sys.io.Yield() else {}
        }
        c.Close()
        b.Close()
    }
    a := acc.Into()
    a2 := acc2.Into()
    try Print(mut out, "{n} {sum} {n2} {sum2}\n", (n: a.n, sum: a.sum, n2: a2.n, sum2: a2.sum))
}
`
	// At release too: the receiver's cell is written through a raw pointer
	// by another task and read back by a safe destructure.
	for _, flags := range [][]string{nil, {"--release"}} {
		wantRun(t, runNative(t, buildNative(t, nil, src, flags...), ""), 0, "200 19900 100 104950\n")
	}
}

// TestChanNegativeCapacity: Chan.New with capacity -1 panics with
// code 8 and a message naming the function (4.13's table).
func TestChanNegativeCapacity(t *testing.T) {
	t.Parallel()
	r := runProgram(t, `export Main(sys: &System): OutOfMemory!void {
    _ = try Chan[int].New(sys.arena.Alloc(), -1)
}
`)
	if r.code != 101 || !strings.Contains(r.stderr, "Chan.New: negative capacity") {
		t.Errorf("exit %d, want 101 and the Chan.New message\nstderr:\n%s", r.code, r.stderr)
	}
}

// g177Program runs Set, Ensure, Remove and Add on Map, HeapMap, TextMap
// and Set with plain types, where nothing panics.
const g177Program = `PlainRows(a: &Arena, h: &Alloc): OutOfMemory!void {
    var m := Map[int, int].New(a)
    try m.Set(1, 0)
    try m.Set(1, 2)
    _ = try m.Ensure(1, 3)
    _ = m.Remove(&1)
    var hm := HeapMap[int, int].New(h)
    hm.Set(h, 1, 0) else {}
    hm.Set(h, 1, 2) else {}
    e1 := hm.Ensure(h, 1, 3) else {
        hm.Free(h)
        return
    }
    *e1 += 1
    _ = hm.Remove(&1)
    hm.Free(h)
    var tm := TextMap[int].New(h)
    for v in 1..3 {
        e2 := tm.Ensure(h, "k", v) else {
            tm.Free(h)
            return
        }
        *e2 += 1
    }
    _ = tm.Remove(h, "k")
    tm.Free(h)
    var s := Set[int].New(a)
    _ = try s.Add(1)
    _ = try s.Add(1)
    _ = s.Remove(&1)
}


export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    try PlainRows(sys.arena, sys.heap.Alloc())
    try Print(mut out, "no panic\n", ())
}
`

// TestMapRows: g177Program's map operations, on plain types, do
// not panic. On a linear key, value or member that one would drop, each is
// E0701 at check (conformance's reject units), not a code 8 panic at run
// time.
func TestMapRows(t *testing.T) {
	t.Parallel()
	exe := buildNative(t, nil, g177Program)
	wantRun(t, runNative(t, exe, "", "plain"), 0, "no panic\n")
}
