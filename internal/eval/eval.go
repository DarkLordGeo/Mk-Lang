// Why: consts, gen bodies and compile-time checks need their values while
// the program is still being compiled; the compiler needs one evaluator with
// a small public face that the driver, check and gen can share.
//
// What: Config, Program, Body and Evaluator, with Eval and Run (Run takes a
// Host), Value, and the stack limit DefaultStackLimit.
//
// How: an Evaluator holds the shared state of a session (compiled bodies,
// type info, const slots); each request gets its own Machine, so evaluations
// run in parallel. The Program interface serves bodies and layouts that are
// already built, so an evaluation never waits on another pass.

// Package eval is the compile-time evaluator: a MIR interpreter over flat
// target-layout memory. Pointer bytes are never read as integers, and raw
// access is allowed only inside the evaluation's own allocations.
package eval

import (
	"sync"
	"sync/atomic"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// Config is everything an Evaluator reads. The tables are the session's; the
// Evaluator never writes Items. It interns into Consts, and into Types only
// the array types [N]T (with the ConstArg N) that serialized pointees need
// through Table.Array; it never changes an existing entry.
type Config struct {
	Types    *types.Table
	Items    *types.Items
	Consts   *types.ConstTable
	Interner *source.Interner
	Program  Program
	Known    types.KnownItems
	Target   types.Target
	// TargetItem is the const item the universe name Target lowers to
	// (RvConstRef); reading it records ReadTarget for arch, os and
	// pointer_bytes.
	TargetItem types.ItemID
	// StackLimit is the frame-byte limit; 0 means DefaultStackLimit.
	StackLimit uint64
	// PageAlloc and PageFree are the std page allocator's allocate
	// (size: int): *u8 and free (p: *u8, size: int), which the evaluator
	// replaces: a fresh zeroed allocation, and marking it dead. EvalArena is
	// the std function (): &Arena that EvalArena() calls once per segment to
	// build its arena over them. Evaluating is std.core's (): bool, false natively,
	// which the evaluator answers true. NoItem when std does not name them.
	PageAlloc, PageFree, EvalArena, Evaluating types.ItemID
}

// DefaultStackLimit is 1 MiB of target-layout frame bytes, a
// fixed limit, not a measurement. Exceeding it is PanicStackLimit, E0901.
const DefaultStackLimit = 1 << 20

// Program is the evaluator's view of the build, implemented by the driver.
// Every method is safe for concurrent use and deterministic, and none of
// them waits on another goroutine's check, lower or borrow work or calls
// back into the Evaluator: everything an evaluation may ask for is ready
// before the request reaches eval.
type Program interface {
	// Body returns the MIR the evaluator runs for fn (a function, method,
	// const or global initializer, contract or fn literal item). With args
	// empty and a non-generic item it is the item's own body; with args, the
	// instance body mono substitutes. eval asks only for what
	// the stage rule lets the evaluation enter.
	Body(fn types.ItemID, args []types.GenericArg) Body
	// Layout is the full layout of a concrete type, and the hash of its shape
	// part for read sets (zero before iface exists). A type with a KParam,
	// KSelf, KInfer or KPoison inside is never asked for. eval asks for the
	// layout of a type only after the stage rule allowed it.
	Layout(t types.TypeID) (*types.Layout, source.Hash)
	// LayoutName spells the generic arguments of a ReadLayout read's Name:
	// iface's Hasher.LayoutName ("" for none). eval cannot import iface.
	LayoutName(args []types.GenericArg) string
	// Const is the value of a const or global of a module other than the
	// request's: always final, because that module is strictly lower.
	Const(item types.ItemID) Const
	// ConstHash is the stable hash of an interned value (iface's; zero before
	// iface exists), recorded in ReadValue.
	ConstHash(c types.ConstID) source.Hash
	// TargetHash is the hash a ReadTarget read of field f records: iface's
	// TargetReadHash for the build's target, so a cached evaluation's read
	// compares equal (zero before iface exists).
	TargetHash(f types.TargetField) source.Hash
}

// FreeImpls is an optional extension of Program for std.raw.Release and
// std.raw.HasFree. mono rewrites both in every instance body and in every
// non-generic body it builds, so eval meets them only in a body the Program
// serves as lowered: a non-generic std body that calls them directly. There
// eval resolves them itself through this lookup (release.go); a Program
// without it makes such a call an internal error.
type FreeImpls interface {
	// FreeImpl resolves the method Free of t's impl of std.core's interface
	// Free on the closed type t, with the impl's generic arguments, as
	// mono's ImplMethod does; ok is false when t has none or std.core
	// declares no interface Free. reads are the lookup's reads (ReadImpl,
	// or ReadNoImpl and ReadNoItem), added to the evaluation's read set.
	FreeImpl(t types.TypeID) (fn types.FnRef, ok bool, reads []types.Read)
}

// BodyState says whether a body can run.
type BodyState uint8

const (
	BodyReady BodyState = iota
	// BodyMissing: no body (a bodiless extern or intrinsic, an interface
	// method); the evaluator implements it, refuses it, or raises an ICE.
	BodyMissing
	// BodyPoisoned: for an item of a lower module, its module has a
	// diagnostic; for an item of the request's module, this body has one;
	// the evaluation yields poison.
	BodyPoisoned
	// BodyNotLowered: not lowered yet. eval never asks for such a body
	// so seeing it is an ICE naming the item.
	BodyNotLowered
)

// Body is one runnable body: its MIR, the hash recorded in ReadBody, the
// reads its construction made, and borrow's onerr decisions.
type Body struct {
	MIR   *mir.Body
	Hash  source.Hash
	State BodyState
	// Reads are facts the served MIR depends on beyond Hash, recorded with
	// ReadBody on every entry of the body. A mono instance's resolved
	// std.raw.Release and folded std.raw.HasFree depend on whether T has an
	// impl of std.core.Free, which the generic body's hash does not cover:
	// the Program puts that lookup's ReadImpl or ReadNoImpl
	// here, so adding or removing the impl invalidates a cached evaluation.
	Reads []types.Read
	// OnerrRuns is borrow.Facts.GuardDecisions(len(MIR.Blocks)), indexed by
	// BlockID: whether the onerr body at a TermOnerrGuard's Yes runs.
	// Nil when the body has no TermOnerrGuard. A TermOnerrGuard reached with
	// OnerrRuns nil is an ICE.
	OnerrRuns []bool
}

// Const is a lower module's const: its value, or poisoned.
type Const struct {
	Value    types.ConstID
	Poisoned bool
}

// Evaluator runs evaluations. It holds the session's const slots and
// compiled code; one per session.
type Evaluator struct {
	cfg        Config
	stackLimit uint64
	pool       bytePool

	codeMu sync.Mutex
	code   map[*mir.Body]*compiled
	// tcode is the tagged mode's compiled form of each body: every block on
	// the general path, none charged once, so the plain mode's code and its
	// flat path stay as they are.
	tcode map[*mir.Body]*compiled

	tiMu sync.RWMutex
	ti   map[types.TypeID]*tinfo
	walk map[types.TypeID][]types.TypeID
	// inter caches the tagged mode's interior ranges per type (taginterior.go).
	inter map[types.TypeID][]irange

	slotMu sync.Mutex
	slots  map[types.ModuleID]*moduleSlots

	// preEntries counts block entries charged once, for tests that
	// check both charging paths ran.
	preEntries atomic.Uint64
	// noFlat makes every statement and terminator take the general path and
	// no block be charged once: the reference the fuzzer compares the flat
	// form with. Set only by tests, before the first evaluation.
	noFlat bool
}

// New returns an Evaluator.
func New(cfg Config) *Evaluator {
	e := &Evaluator{cfg: cfg, stackLimit: cfg.StackLimit}
	if e.stackLimit == 0 {
		e.stackLimit = DefaultStackLimit
	}
	e.code = make(map[*mir.Body]*compiled)
	e.ti = make(map[types.TypeID]*tinfo)
	e.walk = make(map[types.TypeID][]types.TypeID)
	e.slots = make(map[types.ModuleID]*moduleSlots)
	return e
}

// Eval implements types.Evaluator. It is safe for concurrent use: each call
// gets its own memory and frames; const slots and compiled code are shared.
// The result is deterministic: equal requests over equal programs give equal
// results, whatever the goroutine schedule.
//
// Outcomes: Value != NoConst and Err == nil is a value; Err != nil is a
// failure the caller reports; Value == NoConst and Err == nil is poison.
// A const's own failure is returned as Err only to its declaration
// evaluation (StageBodies); every other request that reaches it gets poison,
// so one cause gives one diagnostic.
func (e *Evaluator) Eval(req *types.EvalRequest) types.EvalResult { return e.Run(req, nil) }

// Run is Eval with a Host that implements extra intrinsics (the generator
// API). gen calls it with req.What == EvalGen; h may be nil for any other
// request. A call to an intrinsic neither eval nor h implements is an ICE.
//
// An EvalGen run that completes returns the interned void value (Value !=
// NoConst, Err nil), and its output is the host's; a poisoned run returns
// Value NoConst with Err nil, as every poisoned request does, and its
// partial output must be discarded.
func (e *Evaluator) Run(req *types.EvalRequest, h Host) types.EvalResult {
	m := e.newMachine(req, h)
	defer m.finish()
	return m.request()
}

// Value returns the final value of a const or global item of any module this
// Evaluator ran the declaration evaluation of (StageBodies), and whether it
// has one. The driver copies values into ConstDecl and GlobalDecl after the
// module's declaration evaluations, on one goroutine. A slot that
// another declaration evaluation's readiness walk settled answers
// before its own declaration evaluation, with the outcome that one stores.
func (e *Evaluator) Value(item types.ItemID) (types.ConstID, bool) {
	it := e.cfg.Items.Get(item)
	ms := e.moduleSlots(it.Module)
	ms.mu.Lock()
	defer ms.mu.Unlock()
	s := ms.m[item]
	if s == nil || s.body == nil || s.body.value == types.NoConst {
		return types.NoConst, false
	}
	return s.body.value, true
}

// stop ends an evaluation: an EvalError (completed with the trace where it is
// caught), or poison. It travels as a Go panic inside the interpreter and is
// recovered by the machine; every other panic (an ICE) passes through.
type stop struct {
	err    *types.EvalError
	poison bool
	// entered is the item a stage stop entered or the type item whose layout
	// it read, NoItem otherwise; enteredAt its span (the call site).
	entered   types.ItemID
	enteredAt source.Span
	// chain is a stored stage stop's chain from a nested const's slot, put
	// after the reader's frames.
	chainItems []types.ItemID
	chainSpans []source.Span
	// cycleFrom is the first segment of a cycle (EvalCycle).
	cycleFrom int
	// tagged is a tagged run's own stop (tagViolation, tagSkipped; tagged.go),
	// which stores no slot outcome; skip says why a run was Skipped.
	tagged uint8
	skip   string
}

func refuse(r types.Refusal, msg string) {
	panic(&stop{err: &types.EvalError{Kind: types.EvalRefused, Refusal: r, Message: msg}})
}

func panicWith(c types.PanicCode, msg string) {
	panic(&stop{err: &types.EvalError{Kind: types.EvalPanic, Panic: c, Message: msg}})
}

func poison() { panic(&stop{poison: true}) }
