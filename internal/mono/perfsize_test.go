// Why: a small program should not link the whole I/O table or the task
// scheduler when it never uses them.
// What: tests of deadio.go and tasks.go: an IoTable entry is collected only
// when its field is read, and std.rt.UsesTasks() folds to false unless a
// non-runtime root reaches a task construct, with the runtime entries held
// back while it is false.
// How: collects small std stand-ins and checks which instances exist.

package mono

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// withKeys points the package's std keys at module m for one test.
// Module m also stands for std.raw (rawModule) and its field send is a task
// field (taskFields).
func withKeys(t *testing.T) {
	t.Helper()
	io, ut, tk, lk, rm, tf := keyIoTable, keyUsesTasks, taskKeys, lazyKeys, rawModule, taskFields
	keyIoTable = types.ItemKey("m", "IoTable")
	keyUsesTasks = types.ItemKey("m", "UsesTasks")
	taskKeys = []types.Key{types.ItemKey("m", "WaitWord")}
	lazyKeys = map[string]bool{"m.Lazy": true}
	rawModule = "m"
	taskFields = map[string]bool{"send": true}
	t.Cleanup(func() { keyIoTable, keyUsesTasks, taskKeys, lazyKeys, rawModule, taskFields = io, ut, tk, lk, rm, tf })
}

const ioSrc = `
struct IoTable {
    open: fn(int): int
    write: fn(int): int
    send: fn(int): int
}

OpenE(x: int): int { return x + 1 }
WriteE(x: int): int { return x + 2 }
SendE(x: int): int { return x + 3 }

const Table = IoTable(open: OpenE, write: WriteE, send: SendE)

Write(t: &IoTable, x: int): int {
    f := t.write
    return f(x)
}

UsesTasks(): bool

WaitWord(x: int): int { return x }

Lazy(): int { return 7 }

Start(): int {
    if UsesTasks() {
        return Lazy()
    }
    return 0
}

Main(): int {
    return Write(&Table, 1)
}

MainWaits(): int {
    return WaitWord(Write(&Table, 1))
}

Of[T](p: &T): *T
Read[T](p: *T): T
Addr[T](p: *T): uint
FromAddr[T](a: uint): *T

SendOn(t: &IoTable, x: int): int {
    f := t.send
    return f(x)
}

// First reinterprets the table (Read[IoTable]): every entry is released.
First(): int {
    t := Read(Of(&Table))
    _ = t
    return 0
}

MainRawThenSend(): int {
    _ = First()
    return SendOn(&Table, 1)
}

MainRawOnly(): int {
    return First()
}

MainSend(): int {
    return SendOn(&Table, 1)
}

// PS-5: the table's address becomes a pointer of another type.
MainAddr(): int {
    a := Addr(Of(&Table))
    f := Read(FromAddr[fn(int): int](a + 16))
    return f(1)
}

// PS-5: an address only compared reinterprets nothing.
MainAddrCompared(): int {
    if Addr(Of(&Table)) != Addr(Of(&Table)) {
        return 1
    }
    return Write(&Table, 1)
}

Ext(p: *IoTable): int

// PS-5: an extern function gets the table.
MainExtern(): int {
    return Ext(Of(&Table))
}

Eager(): int { return 5 }

// PS-3: b is a var parameter assigned an immediate once; the switch on it
// before that assignment is on the caller's argument.
F(var b: bool): int {
    if UsesTasks() {
        return 1
    }
    var r := 0
    if b {
        r = Eager()
    }
    b = false
    return r
}

MainParam(): int {
    return F(true)
}

// PS-9: a negation of the fact folds too.
MainNot(): int {
    if !UsesTasks() {
        return 1
    }
    return Eager()
}

// PS-9: a folded node a user root reaches, which reads a field only in its
// true arm.
Start2(): int {
    if UsesTasks() {
        return SendOn(&Table, 1)
    }
    return 0
}

MainSettle(): int {
    return WaitWord(Start2())
}

// PS-9: its call of Eager is made an s.go by the test.
MainSpawn(): int {
    return Eager()
}
`

func collectIo(t *testing.T, main string) (*srcWorld, *Instances) {
	t.Helper()
	withKeys(t)
	sw := newSrcWorldAt(t, "m", true, ioSrc)
	setTable(t, sw)
	sw.items.Get(sw.item(t, "Ext")).Decl.(*types.FuncDecl).Sig.Extern = true
	roots := []Root{{Kind: RootMain, Item: sw.item(t, main)}, {Kind: RootRuntime, Item: sw.item(t, "Start")},
		{Kind: RootRuntime, Item: sw.item(t, "Lazy")}}
	in, ds := Collect(ioEnv{sw}, roots, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) > 0 {
		sw.fail(t, "collect", ds)
	}
	return sw, in
}

// TestPerfSize_DeadIoEntries: only the read field's function is collected;
// the unread entries stay out of the table (codegen stubs them).
func TestPerfSize_DeadIoEntries(t *testing.T) {
	sw, in := collectIo(t, "Main")
	if len(instancesOf(in, sw.item(t, "WriteE"))) != 1 {
		t.Errorf("WriteE (field write, read by Write) not collected")
	}
	for _, name := range []string{"OpenE", "SendE"} {
		if n := len(instancesOf(in, sw.item(t, name))); n != 0 {
			t.Errorf("%s: %d instances, want 0 (its field is never read)", name, n)
		}
	}
}

// TestPerfSize_UsesTasksFalse: nothing reached from Main is a task
// construct, so Start's UsesTasks() folds to false, its true arm is pruned
// and the lazy runtime root is never added.
func TestPerfSize_UsesTasksFalse(t *testing.T) {
	sw, in := collectIo(t, "Main")
	if in.UsesTasks() {
		t.Fatalf("UsesTasks: true, want false")
	}
	if n := len(instancesOf(in, sw.item(t, "Lazy"))); n != 0 {
		t.Errorf("Lazy: %d instances, want 0 (a lazy root, and Start's true arm is pruned)", n)
	}
	st := instancesOf(in, sw.item(t, "Start"))
	if len(st) != 1 {
		t.Fatalf("Start: %d instances", len(st))
	}
	for bi := range st[0].Body.Blocks {
		if tm := &st[0].Body.Blocks[bi].Term; tm.Kind == mir.TermCall {
			t.Errorf("Start still calls item %d after the fold", tm.Callee.Fn.Item)
		}
	}
}

// TestPerfSize_UsesTasksTrue: a task item reached from the root makes the
// fact true: Start is rebuilt with true and the lazy root is collected.
func TestPerfSize_UsesTasksTrue(t *testing.T) {
	sw, in := collectIo(t, "MainWaits")
	if !in.UsesTasks() {
		t.Fatalf("UsesTasks: false, want true (WaitWord is reached from the root)")
	}
	if n := len(instancesOf(in, sw.item(t, "Lazy"))); n != 1 {
		t.Errorf("Lazy: %d instances, want 1", n)
	}
}

// TestPerfSize_RuntimeRootsDoNotDecide: the same task item reached only
// from a runtime root leaves the fact false.
func TestPerfSize_RuntimeRootsDoNotDecide(t *testing.T) {
	withKeys(t)
	sw := newSrcWorldAt(t, "m", true, ioSrc)
	setTable(t, sw)
	roots := []Root{{Kind: RootMain, Item: sw.item(t, "Main")}, {Kind: RootRuntime, Item: sw.item(t, "MainWaits")},
		{Kind: RootRuntime, Item: sw.item(t, "Start")}}
	in, ds := Collect(ioEnv{sw}, roots, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) > 0 {
		sw.fail(t, "collect", ds)
	}
	if in.UsesTasks() {
		t.Errorf("UsesTasks: true from a runtime root's reach, want false")
	}
}

// TestPerfSize_ConstBoolJoin: the join of `UsesTasks() && x` folds too, so
// the call under it is pruned (foldConstBools).
func TestPerfSize_ConstBoolJoin(t *testing.T) {
	withKeys(t)
	sw := newSrcWorldAt(t, "m", true, `
UsesTasks(): bool

Lazy(): int { return 7 }

Start(x: bool): int {
    if UsesTasks() && x {
        return Lazy()
    }
    return 0
}
`)
	in, ds := Collect(sw, []Root{{Kind: RootMain, Item: sw.item(t, "Start")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) > 0 {
		sw.fail(t, "collect", ds)
	}
	if n := len(instancesOf(in, sw.item(t, "Lazy"))); n != 0 {
		t.Errorf("Lazy: %d instances, want 0 (under a false &&)", n)
	}
}

// setTable gives const Table its value as eval would: three fn words.
func setTable(t *testing.T, sw *srcWorld) {
	t.Helper()
	d := sw.items.Get(sw.item(t, "Table")).Decl.(*types.ConstDecl)
	var rs []types.Reloc
	for i, name := range []string{"OpenE", "WriteE", "SendE"} {
		rs = append(rs, types.Reloc{Offset: uint32(8 * i), Kind: types.RelocFn, Fn: types.FnRef{Item: sw.item(t, name)}})
	}
	d.Value = sw.consts.Intern(types.ConstValue{Type: d.Type, Align: 8, Bytes: make([]byte, 24), Relocs: rs})
	d.HasValue = true
}

// ioEnv is the source world with IoTable's field layout (the test world's
// Layout gives sizes only): three fn words at 0, 8 and 16.
type ioEnv struct{ *srcWorld }

func (e ioEnv) Layout(t types.TypeID) *types.Layout {
	l := e.srcWorld.Layout(t)
	if ty := e.tab.Get(t); ty.Kind == types.KNamed && e.items.Get(ty.Item).Key == keyIoTable {
		c := *l
		c.Shape.Size = 24
		c.Fields = []types.FieldLayout{{Offset: 0}, {Offset: 8}, {Offset: 16}}
		return &c
	}
	return l
}

// TestConstArgArithmetic: `Get(N - 1, x)` in a body generic over
// N gives Get the constant N - 1; it was an ICE ("const parameter argument
// is not a constant").
func TestConstArgArithmetic(t *testing.T) {
	src := `Get(const i: int, x: int): int {
    return x + i
}

F[const N: int](x: int): int {
    return Get(N - 1, x) + Get(N * 2 + 1, x)
}

Root(x: int): int {
    return F[4](x)
}
`
	sw := newSrcWorldAt(t, "m", false, src)
	in, ds := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	var names []string
	for _, i := range instancesOf(in, sw.item(t, "Get")) {
		names = append(names, i.Name)
	}
	if len(names) != 2 {
		t.Fatalf("Get instances %v, want the constants 3 and 9", names)
	}
	t.Log(names)
}

// TestReinterpretKeepsFieldReads: after one body reinterprets the table
// (every entry released), a later body's read of a task field still makes
// UsesTasks fact true, and so does the reinterpreting body alone (any entry can
// be called from it).
func TestReinterpretKeepsFieldReads(t *testing.T) {
	for _, main := range []string{"MainRawThenSend", "MainRawOnly"} {
		t.Run(main, func(t *testing.T) {
			sw, in := collectIo(t, main)
			if !in.UsesTasks() {
				t.Errorf("UsesTasks: false, want true (a task field is read, or the table reinterpreted)")
			}
			if n := len(instancesOf(in, sw.item(t, "SendE"))); n != 1 {
				t.Errorf("SendE: %d instances, want 1 (every entry is released)", n)
			}
		})
	}
}

// TestVarParamNotFolded: a var bool parameter assigned an immediate
// once keeps its switches: the one before the assignment is on the
// argument, so Eager is collected and F still switches.
func TestVarParamNotFolded(t *testing.T) {
	sw, in := collectIo(t, "MainParam")
	if in.UsesTasks() {
		t.Fatalf("UsesTasks: true, want false")
	}
	if n := len(instancesOf(in, sw.item(t, "Eager"))); n != 1 {
		t.Errorf("Eager: %d instances, want 1 (if b is on the caller's true)", n)
	}
	fs := instancesOf(in, sw.item(t, "F"))
	if len(fs) != 1 {
		t.Fatalf("F: %d instances", len(fs))
	}
	sw2 := 0
	for bi := range fs[0].Body.Blocks {
		if fs[0].Body.Blocks[bi].Term.Kind == mir.TermSwitch {
			sw2++
		}
	}
	if sw2 == 0 {
		t.Errorf("F has no switch left: the switch on parameter b was folded")
	}
}

// TestAddrEscapeReadsAll: an IoTable's address used as an integer (here
// made into a fn pointer) releases every entry; one only compared releases
// none; a table pointer passed to an extern function releases every entry.
func TestAddrEscapeReadsAll(t *testing.T) {
	for _, c := range []struct {
		main string
		want int
	}{{"MainAddr", 1}, {"MainAddrCompared", 0}, {"MainExtern", 1}} {
		t.Run(c.main, func(t *testing.T) {
			sw, in := collectIo(t, c.main)
			for _, name := range []string{"OpenE", "SendE"} {
				if n := len(instancesOf(in, sw.item(t, name))); n != c.want {
					t.Errorf("%s: %d instances, want %d", name, n, c.want)
				}
			}
		})
	}
}

// TestTaskFieldMakesTrue: a read of a task field (send) from Main makes
// the fact true, and the lazy root is collected.
func TestTaskFieldMakesTrue(t *testing.T) {
	sw, in := collectIo(t, "MainSend")
	if !in.UsesTasks() {
		t.Fatalf("UsesTasks: false, want true (send is a task field)")
	}
	if n := len(instancesOf(in, sw.item(t, "Lazy"))); n != 1 {
		t.Errorf("Lazy: %d instances, want 1", n)
	}
}

// TestNegationFolds: `if !UsesTasks()` folds under false, so the call
// after it is pruned.
func TestNegationFolds(t *testing.T) {
	sw, in := collectIo(t, "MainNot")
	if in.UsesTasks() {
		t.Fatalf("UsesTasks: true, want false")
	}
	if n := len(instancesOf(in, sw.item(t, "Eager"))); n != 0 {
		t.Errorf("Eager: %d instances, want 0 (under !false)", n)
	}
}

// TestSettleRebuildsUserReached: Start2, reached from the user root and
// built with false, is rebuilt with true once WaitWord makes the fact true;
// the field it reads only in its true arm releases SendE during the rebuild.
func TestSettleRebuildsUserReached(t *testing.T) {
	sw, in := collectIo(t, "MainSettle")
	if !in.UsesTasks() {
		t.Fatalf("UsesTasks: false, want true")
	}
	if n := len(instancesOf(in, sw.item(t, "SendE"))); n != 1 {
		t.Errorf("SendE: %d instances, want 1 (released by the rebuilt Start2)", n)
	}
	st := instancesOf(in, sw.item(t, "Start2"))
	if len(st) != 1 {
		t.Fatalf("Start2: %d instances, want 1", len(st))
	}
	calls := 0
	for bi := range st[0].Body.Blocks {
		if tm := &st[0].Body.Blocks[bi].Term; tm.Kind == mir.TermCall {
			calls++
		}
	}
	if calls == 0 {
		t.Errorf("Start2 kept its false body: no call of SendOn")
	}
}

// TestSpawnMakesTrue: a body with an s.go (a TermCall with Spawn set)
// reached from Main makes the fact true.
func TestSpawnMakesTrue(t *testing.T) {
	withKeys(t)
	sw := newSrcWorldAt(t, "m", true, ioSrc)
	setTable(t, sw)
	gb := sw.bodies[sw.item(t, "MainSpawn")]
	marked := false
	for bi := range gb.Blocks {
		if tm := &gb.Blocks[bi].Term; tm.Kind == mir.TermCall && tm.Callee.Fn.Item == sw.item(t, "Eager") {
			tm.Spawn = 1
			marked = true
		}
	}
	if !marked {
		t.Fatalf("no call of Eager in MainSpawn")
	}
	// A body with a hand-set Spawn is not a valid s.go: no Validate.
	in, ds := Collect(ioEnv{sw}, []Root{{Kind: RootMain, Item: sw.item(t, "MainSpawn")}, {Kind: RootRuntime, Item: sw.item(t, "Start")}},
		Options{Target: types.X86_64Windows})
	if len(ds) > 0 {
		sw.fail(t, "collect", ds)
	}
	if !in.UsesTasks() {
		t.Errorf("UsesTasks: false, want true (Main's body holds an s.go)")
	}
}

// TestTaskKeysHoldPollBackend: task.PollBackend reads the TaskState page,
// which only a build with tasks maps, so a program whose first scheduler
// call is PollBackend must link the scheduler; without the key it read
// null + 0x70.
func TestTaskKeysHoldPollBackend(t *testing.T) {
	want := types.ItemKey("std.task", "PollBackend")
	for _, k := range taskKeys {
		if k == want {
			return
		}
	}
	t.Errorf("taskKeys has no %v", want)
}
