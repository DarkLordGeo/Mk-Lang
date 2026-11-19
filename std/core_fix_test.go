// Why: regression tests for bugs found in std.core's erasure and ports.
// What: alignment, Send and Share bounds, stress under seeds and std.sim, the bench probe.
// How: the fixtures run in std.coreprobe, as core_erasure_test.go's do;
// core_cancel_test.go holds the cancel-safety tests.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// core8Align: ErasedCall.Of over a one-word F whose alignment is not a
// word's, and word-aligned F values through Of and Fn.
const core8Align = `import std.core

struct St {
    n: int
}

Id(s: &St, x: int): int {
    return s.n + x
}

test "ALIGN-1 ErasedCall.Of over an [8]u8 panics" (sys: &System) {
    s := St(n: 1)
    a: [8]u8 = [1; 8]
    _ = core.ErasedCall.Of[St, [8]u8](&s, a)
}

test "ALIGN-2 ErasedCall.Of over a record of two u32 panics" (sys: &System) {
    s := St(n: 1)
    _ = core.ErasedCall.Of[St, (a: u32, b: u32)](&s, (a: 1, b: 2))
}

test "ALIGN-3 a fn value and a u64 come back from Fn as they went into Of" (sys: &System) {
    s := St(n: 40)
    call := core.ErasedCall.Of[St, fn(&St, int): int](&s, Id)
    f := call.Fn[St, fn(&St, int): int]()
    Assert(f(call.State[St, fn(&St, int): int](), 2) == 42)
    w := core.ErasedCall.Of[St, u64](&s, 0x0102030405060708)
    Assert(w.Fn[St, u64]() == 0x0102030405060708)
}
`

// core8Bounds: the bounds the erasure's Send and Share rest on, and that a
// port Reader and a Filtered of a non-Send state cannot cross to a task.
// Each line is refused.
const core8Bounds = `import std.core

struct CellHolder {
    c: Cell[int]
}

BoxCell(b: Box[Cell[int]]): core.ErasedBox from b {
    return core.ErasedBox.Of[Cell[int]](b)
}

BoxHolder(b: Box[CellHolder]): core.ErasedBox from b {
    return core.ErasedBox.Of[CellHolder](b)
}

UseCell(c: &Cell[int]) {
}

CallCell(c: &Cell[int]): core.ErasedCall from c {
    return core.ErasedCall.Of[Cell[int], fn(&Cell[int])](c, UseCell)
}

FillCell(b: Box[?Cell[int]], c: Cell[int]): core.ErasedBox from b, c {
    return core.ErasedBox.Fill[Cell[int]](b, c)
}
`

// core8Crossing: a Filtered over a state that is not Send, and a port
// Reader, cannot be sent to a task.
const core8Crossing = `import std.core

UseFiltered(f: core.Filtered[&Cell[int]]) {
}

SendFiltered(io: Io, c: &Cell[int]) {
    f := core.Filtered[&Cell[int]].New(c)
    scope s := io.Scope() {
        s.go UseFiltered(f)
    }
}

UseReader(r: Reader) {
}

SendReader(io: Io) {
    s := Src(text: "ab", pos: Mutex[int].New(0))
    var b: [4]u8 = [0; 4]
    r := s.Reader(io, mut b[..])
    scope sc := io.Scope() {
        sc.go UseReader(r)
    }
}
`

// core8SimStress is the ERASE-1 and PORT-3 blocks' shapes under std.sim, one
// seeded schedule per seed. A Sim runs one task at a time and
// switches only at an Io operation, so the transport and the sink yield
// while they hold their lock: another task then runs inside the
// operation, and contends the Conn's locks and the sink's. Each test
// counts the seeds whose run interleaved and requires some, so a run that
// never switched cannot pass.
const core8SimStress = `import std.core
import std.sim

// SimMem is Mem for std.sim: each operation yields while it holds the
// lock, and Read reports the bytes written so far.
linear struct SimMem {
    total: Mutex[int]
}

impl Transport for SimMem {
    Read(&self, io: Io, buf: mut []u8): Error!int {
        g := try self.total.Lock(io)
        n := *g.Ref()
        io.Yield() else {
            g.Unlock()
            fail Canceled
        }
        g.Unlock()
        for i in 0..8 {
            buf[i] = Trunc[u8](n >> (8 * i))
        }
        return 8
    }
    WriteAll(&self, io: Io, data: []u8): Error!void {
        var g := try self.total.Lock(io)
        io.Yield() else {
            g.Unlock()
            fail Canceled
        }
        *g.Mut() += data.Len()
        g.Unlock()
    }
    Close(&self, io: Io): Error!void {
    }
    Abandon(self) {
        SimMem(total) := self
        _ = total.Into()
    }
}

// SimTotal is the total a SimMem's Read reports.
SimTotal(c: &Conn, io: Io): Error!int {
    var buf: [8]u8 = [0; 8]
    n := try c.Read(io, mut buf[..])
    Assert(n == 8)
    var total := 0
    for i in 0..8 {
        total |= int(buf[i]) << (8 * i)
    }
    return total
}

// Add adds k to m.
Add(io: Io, m: &Mutex[int], k: int) {
    var g := m.Lock(io) else {
        return
    }
    *g.Mut() += k
    g.Unlock()
}

// SimHammer is Hammer with 4 rounds of 6 bytes each: a write, a read, and
// a write through the Conn's own Writer. A round whose read does not see
// exactly this task's 6 bytes since its last read saw another task's
// writes in between: it counts in mixed.
SimHammer(io: Io, c: &Conn, fails: &Mutex[int], mixed: &Mutex[int]) {
    var wb: [3]u8 = [0; 3]
    var last := -1
    for _ in 0..4 {
        c.WriteAll(io, "abcd".AsBytes()) else {
            Add(io, fails, 1)
            return
        }
        total := SimTotal(c, io) else {
            Add(io, fails, 1)
            return
        }
        if last >= 0 && total != last + 6 {
            Add(io, mixed, 1)
        }
        last = total
        var w := c.Writer(io, mut wb[..])
        w.Str("xy") else {
            Add(io, fails, 1)
            return
        }
        w.Flush() else {
            Add(io, fails, 1)
            return
        }
    }
}

test "ERASE-1 under std.sim: an erased transport shared by 16 tasks, over 1,000 seeds" (sys: &System) {
    var interleaved := 0
    for seed in 0..1000 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer sm.Free()
        io := sm.Io()
        m := SimMem(total: Mutex[int].New(0))
        c := NewConn(sys.heap, m) else e {
            m.Abandon()
            fail e
        }
        onerr c.Abandon()
        fails := Mutex[int].New(0)
        mixed := Mutex[int].New(0)
        scope s := io.Scope() {
            for _ in 0..16 {
                s.go SimHammer(io, &c, &fails, &mixed)
            }
        }
        Assert(fails.Into() == 0)
        if mixed.Into() > 0 {
            interleaved += 1
        }
        Assert(try SimTotal(&c, io) == 16 * 4 * 6)
        try c.Close(io)
    }
    // Some schedules ran another task's writes between a task's reads.
    Assert(interleaved > 0)
}

// Tally is what a SimSink has been given: the bytes of each task's
// letter, and how often a write came from another task than the last.
struct Tally {
    counts: [16]int
    last: int
    switches: int
}

// SimSink is Sink for std.sim: a write yields before it takes the lock and
// while it holds it.
struct SimSink {
    t: Mutex[Tally]
}

SimSinkWrite(s: &SimSink, io: Io, b: []u8): WriteError!void {
    // A yield before the lock as well: with only the one inside, the
    // writer that holds the lock takes it again before a woken waiter
    // runs, and the writes would arrive one task after another.
    io.Yield() else {
        fail WriteError
    }
    var g := s.t.Lock(io) else {
        fail WriteError
    }
    io.Yield() else {
        g.Unlock()
        fail WriteError
    }
    t := g.Mut()
    for x in b {
        t.counts[int(x) - 0x61] += 1
    }
    if b.Len() > 0 && int(b[0]) != t.last {
        t.switches += 1
        t.last = int(b[0])
    }
    g.Unlock()
}

SimSinkFlush(s: &SimSink, io: Io): WriteError!void {
}

const SimSinkPort = core.WritePort[SimSink].New(SimSinkWrite, SimSinkFlush)

// SimShareSink writes 10 records of its letter (task k writes 'a' + k)
// through its own 5-byte Writer over the shared sink.
SimShareSink(io: Io, s: &SimSink, k: int) {
    var rec: [3]u8 = [0; 3]
    for i in 0..3 {
        rec[i] = Trunc[u8](0x61 + k)
    }
    var b: [5]u8 = [0; 5]
    var w := (&SimSinkPort).Writer(io, s, mut b[..])
    for _ in 0..10 {
        w.Bytes(rec[..]) else {
            return
        }
    }
    w.Flush() else {
        return
    }
}

test "PORT-3 under std.sim: Writers over one shared state from 16 tasks, over 1,000 seeds" (sys: &System) {
    var interleaved := 0
    for seed in 0..1000 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer sm.Free()
        io := sm.Io()
        s := SimSink(t: Mutex[Tally].New(Tally(counts: [0; 16], last: -1, switches: 0)))
        scope sc := io.Scope() {
            for k in 0..16 {
                sc.go SimShareSink(io, &s, k)
            }
        }
        SimSink(t) := s
        tally := t.Into()
        // Every byte of every task arrived, none lost and none mixed in.
        for k in 0..16 {
            Assert(tally.counts[k] == 10 * 3)
        }
        // Sixteen tasks run one after another switch 16 times.
        if tally.switches > 16 {
            interleaved += 1
        }
    }
    Assert(interleaved > 0)
}
`

// core8Run is one run of a stress matrix: MK_CARRIERS, MK_TEST_SEED (0
// leaves the jitter points off), MK_IO ("" the default backend) and the
// build flags.
type core8Run struct {
	carriers, seed int
	io             string
	flags          []string
}

func (r core8Run) String() string {
	return fmt.Sprintf("MK_CARRIERS=%d MK_TEST_SEED=%d MK_IO=%q %s", r.carriers, r.seed, r.io, strings.Join(r.flags, " "))
}

// core8MkMatrix copies std with the fixtures once and runs mk test
// --std=std.coreprobe with the target flags under each run's environment
// (one cache, so a run rebuilds only what its flags change); every block
// of files must pass in every run.
func core8MkMatrix(t *testing.T, fixtures map[string]string, target []string, runs []core8Run, files ...string) {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	for _, r := range runs {
		t.Setenv("MK_CARRIERS", fmt.Sprint(r.carriers))
		t.Setenv("MK_TEST_SEED", fmt.Sprint(r.seed))
		t.Setenv("MK_IO", r.io)
		args := append(append([]string{"test", "--std=std.coreprobe"}, target...), r.flags...)
		out, status := mkCmd(t, root, stdDir, args...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		failed := false
		for _, f := range files {
			for _, name := range core8TestNames(f) {
				if !strings.Contains(out, fmt.Sprintf("PASS std.coreprobe %q", name)) {
					t.Errorf("%s: %q did not pass (status %d)", r, name, status)
					failed = true
				}
			}
		}
		if failed || status != 0 {
			t.Logf("%s: mk test output (status %d):\n%s", r, status, out)
		}
	}
}

// core8Seeds is how many MK_TEST_SEED values a stress run sweeps:
// MK_CORE8_SEEDS, else 8.
func core8Seeds() int {
	if n, err := strconv.Atoi(os.Getenv("MK_CORE8_SEEDS")); err == nil && n > 0 {
		return n
	}
	return 8
}

// TestCore_StressSeeds: the erasure blocks, ERASE-1's and
// PORT-3's 16 contending tasks among them, at 1, 2, 8 and 16 carriers with
// the scheduler's jitter points armed by MK_TEST_SEED 1 to core8Seeds(),
// in dev and in release.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCore_StressSeeds(t *testing.T) {
	var runs []core8Run
	for _, flags := range [][]string{nil, {"--release"}} {
		runs = append(runs, core8Run{carriers: 1, seed: 1, flags: flags})
		for _, carriers := range []int{2, 8, 16} {
			for seed := 1; seed <= core8Seeds(); seed++ {
				runs = append(runs, core8Run{carriers: carriers, seed: seed, flags: flags})
			}
		}
	}
	core8MkMatrix(t, core8Fixtures(map[string]string{"tests_erasure.mk": core8Blocks}), nil, runs, core8Blocks)
}

// TestCore_ErasureUnderSim: ERASE-1 and PORT-3 under
// std.sim over 1,000 seeds each, at 1 and 16 carriers (a Sim runs one task
// at a time; the carriers change only how the baton moves between them).
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCore_ErasureUnderSim(t *testing.T) {
	fx := core8Fixtures(map[string]string{"tests_simstress.mk": core8SimStress})
	core8MkMatrix(t, fx, nil, []core8Run{{carriers: 1}, {carriers: 16}}, core8SimStress)
}

// TestCore_ErasedCallAlignment: ErasedCall.Of refuses a
// one-word F whose alignment is less than a word's, whose bits its aligned
// read could not load, and gives a word-aligned F back as it was.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCore_ErasedCallAlignment(t *testing.T) {
	out, _ := core8MkTest(t, core8Fixtures(map[string]string{"tests_align.mk": core8Align}), 1)
	for _, name := range []string{"ALIGN-1", "ALIGN-2"} {
		re := regexp.MustCompile(`FAIL std\.coreprobe "` + name + `[^"]*": panic[^\n]*\n(?:[^\n]*\n){0,3}?[^\n]*ErasedCall\.Of: not a function value`)
		if !re.MatchString(out) {
			t.Errorf("%s did not panic with ErasedCall.Of's message", name)
		}
	}
	if !strings.Contains(out, `PASS std.coreprobe "ALIGN-3`) {
		t.Error("ALIGN-3 did not pass")
	}
	if t.Failed() {
		t.Logf("output:\n%s", out)
	}
}

// TestCore_SendShareBounds: ErasedBox's unconditional Send
// and Share and ErasedCall's derived ones are sound only through their
// constructors' bounds, and Filtered's conditional ones only because a
// filter Reader stays with its task: each refusal is pinned, exactly.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCore_SendShareBounds(t *testing.T) {
	for _, c := range []struct {
		file, text string
		want       []string
	}{
		{"tests_bounds.mk", core8Bounds, []string{
			"tests_bounds.mk:8:12: E0701 bound-unmet: `Cell[int]` does not satisfy `T: Share`",
			"tests_bounds.mk:12:12: E0701 bound-unmet: `CellHolder` does not satisfy `T: Share`",
			"tests_bounds.mk:19:12: E0701 bound-unmet: `Cell[int]` does not satisfy `T: Share`",
			"tests_bounds.mk:23:12: E0701 bound-unmet: `Cell[int]` does not satisfy `T: Share`", // ErasedBox.Fill
		}},
		{"tests_crossing.mk", core8Crossing, []string{
			"tests_crossing.mk:9:26: E1001 not-send: `Filtered[&Cell[int]]` is not Send",
			"tests_crossing.mk:21:25: E1001 not-send: `Reader` is not Send",
		}},
	} {
		out, status := core8MkTest(t, core8Fixtures(map[string]string{c.file: c.text}), 1)
		if status == 0 {
			t.Fatalf("%s: mk test accepted the uses:\n%s", c.file, out)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("missing %s", w)
			}
		}
		if n := strings.Count(out, ": E"); n != len(c.want) {
			t.Errorf("%s: %d diagnostics, want %d", c.file, n, len(c.want))
		}
		if t.Failed() {
			t.Logf("output:\n%s", out)
		}
	}
}

// TestCore_BenchProbeBuilds: the local
// benchmark's program builds against the probe's Transport (it lacked
// Close), and each kind's checksum is the Go model's at a small n. It
// always runs, so the bench fixture cannot drift from the probe again.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestCore_BenchProbeBuilds(t *testing.T) {
	fx := core8Fixtures(map[string]string{"bench.mk": core8BenchProbe})
	exe := buildNative(t, fx, core8BenchMain)
	const n = 1000
	for _, kind := range core8BenchKinds {
		out, err := exec.Command(exe, kind, fmt.Sprint(n)).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %d: %v\n%s", kind, n, err, out)
		}
		if got, want := strings.TrimSpace(string(out)), fmt.Sprint(core8BenchSum(kind, n)); got != want {
			t.Errorf("%s %d: checksum %s, want %s", kind, n, got, want)
		}
	}
}
