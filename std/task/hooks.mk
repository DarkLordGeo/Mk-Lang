// Why: std.sim runs a program's tasks one at a time in an order drawn from
// a seed, so it must hear every point where a task suspends or resumes, and
// every birth and end, without slowing down native tasks.
//
// What: the calls of an Io's enter and leave hooks around every suspension
// point (HookFirst, HookLast, HookLeave, HookEnter), the birth and note
// facts (HookBorn, OweNote, HookNote), the wait facts (HoldSet, HookHold,
// HookHome...), and the note fan (NoteFanSet).
//
// How: a task's Io is its scope's (SpawnAlloc copies it into t.io), so a
// task spawned under a sim.Sim's Io calls the Sim's hooks. The rest of this
// comment gives where the hooks run, the no-nesting rule, the cost and the
// facts they carry.
//
// Where they run. On the task's own stack: enter before the task's
// function first runs (HookFirst) and at the exit of every suspension
// point; leave at the entry of every suspension point and once when the
// function returns (HookLast). The suspension points are the five leaves
// every park of a task goes through: WaitWord, Await, Blocking, SleepFor
// and YieldNow (Join, Chan, Mutex, the limit gate and every Native* entry
// that waits reach a park only through them). A leaf calls leave before it
// takes a lock or registers a wait, and enter after it unregistered and
// unlocked everything, so a hook runs with no lock held and with none of
// the task's wait state (t.wait, t.op, t.job, t.timer, the blocked list) in
// use, and may itself suspend: std.sim's enter waits there for the task's
// turn. A leaf that returns at once (WaitWord on a changed word) still
// calls both, so every leaf is a point where a Sim may hand its baton.
//
// No nesting. HookBit in t.flags is set from leave to the matching enter
// (and from HookLast to the end); a leaf entered while it is set, the leaf
// a hook itself waits in, calls no hook, so hooks never recurse and the
// task's wait state is never reused before its own leaf cleaned up. t.flags
// is written only by the running task on its own stack (and MainFlag before
// the main task first runs), so it is a plain field.
//
// Cost. SpawnAlloc sets HookedBit once, when the task's table is not the
// native one (core.IoHooked; a comparison with `&NativeTable` cannot
// tell, since a const has no address identity). A task without it
// (the main task and every task of a native scope) pays one load and one
// compare of t.flags per leaf call, and no indirect call: NativeTable's
// hooks are core.InertHook, so skipping them is the same as calling them.
// A Join or a limit gate that waits also pays HoldSet's core.IoHooked load
// and compare on the scope's Io, once per wait.
//
// Facts. Three leave calls carry a fact in the id's top bits
// (task ids, rt.NextTaskId, stay far below 2^62), so a scheduler knows its
// task set exactly; a table that needs none ignores them (core.InertHook):
//   id | BornBit: the task id was just reserved under this Io (SpawnAlloc,
//     after HookMark, on the spawner's stack, before Spawn readies it);
//   id | LastBit: the task's function returned (HookLast);
//   id | BornBit | LastBit (NoteBits): the spawner id, which reserved
//     tasks under this Io since its last leaf, reached a leaf (or its
//     end): it suspends, so it reserves no more for now (HookNote, on the
//     spawner's stack; the spawner may be the main task or a native task,
//     whose leaves call no other hook).
// A scheduler that runs tasks one at a time (std.sim) can then decide
// only when every reserved task has arrived and no spawner outside it is
// still spawning, instead of guessing births from rt.NextTaskId. For the
// waits it cannot see, a leave from Join or a scope's limit gate leaves
// the scope in t.hold (HookHold, HookHoldLimit), and HookHome names the
// scope a task belongs to, so a scheduler can tell when such a wait will
// end without any of its own tasks running; a task that waits so in a
// scope of a hooked Io not its own owes that Io the leaf's note (HoldSet).
// A note is owed to at most two Ios by slot; past them, once a Sim
// exists, it goes through the note fan (NoteFanSet), so no note is sent
// before the leaf; with no fan set a third Io still evicts the
// oldest (OweNote).
//
// Invariant (the Hooks-Pair block of tests_hooks.mk): per task, the first call
// is its born leave, on its spawner's stack, then an enter; every later
// leave is followed by one enter, except the last leave (LastBit); the
// enter and leave calls run on the task named by the id, and a hook that
// suspends calls no hook.

import std.core
import std.raw

const HookBit: u32 = 2
const HookedBit: u32 = 4
// NoteBit: note holds an Io this task spawned into since its last leaf.
const NoteBit: u32 = 8
// GateBit: hold is the limit gate's scope, not a Join's.
const GateBit: u32 = 16
// Note2Bit: note2 holds a second Io owed a note (set only with NoteBit).
const Note2Bit: u32 = 32
// FanBit: a third Io was owed a note since the last leaf, so the next leaf
// also sends one through the note fan (set only with both bits above).
const FanBit: u32 = 64

// NoteFan is the address of the note fan's table, 0 until a scheduler set
// it (NoteFanSet): a note sent to an Io of it is a note to every run of
// that scheduler (std.sim's every live Sim), so a third Io's note needs no
// slot of its own and is never sent early.
var NoteFan: raw.Atomic[uint] = raw.Atomic[uint].New(0)

/// NoteFanSet names t as the note fan: from now on a task owed a note by a
/// third hooked Io before its next leaf sends that leaf's note also to an Io
/// of t (flags 0), whose leave hook must hand it to every scheduler a note
/// may be owed to (std.sim's Sim.New sets it). t must be static.
/// O(1); allocates nothing.
export NoteFanSet(t: *core.IoTable) {
    // Edge: none needed; t is static and the word is only set, to one
    // table (std.sim's FanTable), so a Load that reads it reads a live
    // table and one that reads 0 sends no fan note.
    NoteFan.Store(raw.Addr(t))
}

/// BornBit marks a hook id as a task's born leave (the header).
export const BornBit: u64 = 0x8000_0000_0000_0000
/// LastBit marks a hook id as a task's last leave (the header).
export const LastBit: u64 = 0x4000_0000_0000_0000
/// NoteBits marks a hook id as a spawner's note (the header).
export const NoteBits: u64 = 0xC000_0000_0000_0000

// HookMark sets HookedBit on a fresh task whose Io has hooks (SpawnAlloc).
HookMark(t: *Task) {
    unsafe "t is a fresh task no other thread sees yet" {
        if core.IoHooked((*t).io) {
            (*t).flags = (*t).flags | HookedBit
        }
    }
}

// SameIo: a and b are one Io (table and flags, the two words of core's
// Io).
SameIo(a: *Io, b: *Io): bool {
    unsafe "an Io is two words: its table's address, then its flags (core_iodecl.mk)" {
        x := raw.PtrCast[Io, uint](a)
        y := raw.PtrCast[Io, uint](b)
        return raw.Read(x) == raw.Read(y) && core.IoFlags(*a) == core.IoFlags(*b)
    }
}

// HookBorn is the birth fact, in SpawnAlloc after HookMark: a
// hooked child t is announced under its own Io on the spawner's stack,
// before Spawn readies it, and the spawner owes that Io a note at its next
// leaf (OweNote), each note after its own births. The note is owed before
// the born leave, so a note a third Io evicts (no fan set) is sent before
// the birth, whose leave makes the spawner pending again.
HookBorn(t: *Task) {
    unsafe "t is a fresh task no other thread sees yet; p is the running task" {
        if ((*t).flags & HookedBit) == 0 {
            return
        }
        p := Current()
        if !raw.IsNull(p) {
            OweNote(p, (*t).io)
        }
        core.IoLeave((*t).io, (*HeaderOf(t)).id | BornBit)
    }
}

// OweNote makes p owe io a note at its next leaf. A note already owed to
// another Io stays owed, in note2, so a spawn into a scope of another Io
// (a NoCancel Io, a Recorder's) sends no note before the leaf: a scheduler
// reads a note as the task's suspension. A third Io takes no slot:
// FanBit makes the leaf send its note through the note fan too, which
// reaches every live Sim, so no note is sent before the leaf. A
// third Io with no Sim behind it (another hooked table) then gets no note
// for that leaf; of std's hooked tables only std.task's test table reads
// notes in its leave (the inert hooks, rt's direct table, std.proc's test
// table and a Replayer's tape ignore them), so none loses one that
// matters. With no fan set (no Sim was ever made) a third Io sends the
// oldest note at once instead.
// The caller is p, outside every hook.
OweNote(p: *Task, io: Io) {
    unsafe "p is the running task: its flags and notes are its own" {
        if ((*p).flags & NoteBit) != 0 {
            if SameIo(raw.Of(&(*p).note), raw.Of(&io)) {
                return
            }
            if ((*p).flags & Note2Bit) != 0 {
                if SameIo(raw.Of(&(*p).note2), raw.Of(&io)) {
                    return
                }
                // Edge: NoteFanSet's word names a static table; see its
                // Store.
                if NoteFan.Load() != 0 {
                    (*p).flags = (*p).flags | FanBit
                    return
                }
                (*p).flags = (*p).flags & ~Note2Bit
                core.IoLeave((*p).note2, HookId(p) | NoteBits)
            }
            (*p).note2 = (*p).note
            (*p).flags = (*p).flags | Note2Bit
        }
        (*p).note = io
        (*p).flags = (*p).flags | NoteBit
    }
}

// HookNote sends the notes p owes, note2's first, then the fan's when a
// third Io was owed (every bit clear first, so a leaf inside a note's hook
// sends none, and a note owed from inside one, HookOwe, waits for the next
// leaf). A scheduler may so get two notes of one leaf; each is the same
// fact.
HookNote(p: *Task) {
    unsafe "p is the running task" {
        f := (*p).flags
        n1 := (*p).note
        n2 := (*p).note2
        (*p).flags = f & ~(NoteBit | Note2Bit | FanBit)
        if (f & Note2Bit) != 0 {
            core.IoLeave(n2, HookId(p) | NoteBits)
        }
        core.IoLeave(n1, HookId(p) | NoteBits)
        if (f & FanBit) != 0 {
            // Edge: as at NoteFanSet's Store: a static table, or 0.
            a := NoteFan.Load()
            if a != 0 {
                core.IoLeave(core.MakeIo(raw.FromAddr[core.IoTable](a), 0), HookId(p) | NoteBits)
            }
        }
    }
}

/// HookOwe makes the running task owe io a note at its next leaf, as a
/// birth does, so a scheduler that runs tasks one at a time can wait for
/// a task outside it (the main task) that a wait just returned to: until
/// that task's next leaf it may still wake or spawn tasks (std.sim's
/// SimWait). A note owed to another Io stays owed, as for a birth
/// (OweNote). False when no note is owed: no running task, or one inside a
/// hook. Call it with no lock held. O(1); allocates nothing.
export HookOwe(io: Io): bool {
    p := Current()
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is the running task: its flags and note are its own" {
        if ((*p).flags & HookBit) != 0 {
            return false
        }
    }
    OweNote(p, io)
    return true
}

// HoldSet and HoldClear bracket the WaitWord of Join (gate false) and of
// SpawnAlloc's limit gate (gate true) on t's own stack, so t's leave hook
// can read which scope it waits for (HookHold). When the scope's Io has
// hooks and is not t's own, t owes it a note at that WaitWord's leaf, so
// a scheduler behind it learns of the wait even when t owed it none (its
// last leaf was a wait elsewhere): a run's own tasks tell it through
// their own leave hook.
HoldSet(t: *Task, r: *ScopeRec, gate: bool) {
    if raw.IsNull(t) {
        return
    }
    unsafe "t is the running task; r is a live scope it waits in" {
        (*t).hold = r
        if gate {
            (*t).flags = (*t).flags | GateBit
        }
        if ((*t).flags & HookBit) == 0 && core.IoHooked((*r).io) && !SameIo(raw.Of(&(*t).io), raw.Of(&(*r).io)) {
            OweNote(t, (*r).io)
        }
    }
}

HoldClear(t: *Task) {
    if raw.IsNull(t) {
        return
    }
    unsafe "t is the running task" {
        (*t).hold = raw.Null[ScopeRec]()
        (*t).flags = (*t).flags & ~GateBit
    }
}

/// HookHome is the key of t's scope (its record's address), the scope a
/// Join of which waits for t; for a leave or enter hook, on t's own stack.
/// 0 when t is null. O(1); allocates nothing.
export HookHome(t: *Task): uint {
    if raw.IsNull(t) {
        return 0
    }
    unsafe "t is the running task" {
        return raw.Addr((*t).home)
    }
}

/// HookHold is the key of the scope whose Join or limit gate t waits in, 0
/// in any other leaf; read by t's leave hook on t's own stack.
/// 0 when t is null. O(1); allocates nothing.
export HookHold(t: *Task): uint {
    if raw.IsNull(t) {
        return 0
    }
    unsafe "t is the running task" {
        return raw.Addr((*t).hold)
    }
}

/// HookHoldIo is the Io the scope of HookHold was opened with, so a
/// scheduler can tell whether that scope's tasks are its own; an
/// inert Io when t is null or waits in no Join or gate. Read by t's leave
/// hook on t's own stack: the scope is live, t waits in it. O(1); allocates
/// nothing.
export HookHoldIo(t: *Task): Io {
    if !raw.IsNull(t) {
        unsafe "t is the running task, waiting in hold's Join or gate" {
            if !raw.IsNull((*t).hold) {
                return (*(*t).hold).io
            }
        }
    }
    return core.MakeIo(raw.Of(&core.InertIo), 0)
}

/// HookHoldLimit is the limit of the gate t waits in (it waits until fewer
/// than that many tasks of the scope are live), 0 for a Join (it waits
/// until none is). The scope is live: t waits in it.
/// 0 when t is null. O(1); allocates nothing.
export HookHoldLimit(t: *Task): u32 {
    if raw.IsNull(t) {
        return 0
    }
    unsafe "t is the running task, waiting in hold's gate" {
        return if ((*t).flags & GateBit) == 0 || raw.IsNull((*t).hold) { 0 } else { (*(*t).hold).limit }
    }
}

// HookOwed sets HookBit and reports true when t is a task whose Io has
// hooks and no hook of t is running; false for null (off a task stack), a
// native task, or a leaf inside a hook.
HookOwed(t: *Task): bool {
    if raw.IsNull(t) {
        return false
    }
    unsafe "t is the running task: its flags and io are its own" {
        // Neither native (HookedBit clear) nor inside a hook (HookBit set).
        if ((*t).flags & (HookedBit | HookBit)) != HookedBit {
            return false
        }
        (*t).flags = (*t).flags | HookBit
    }
    return true
}

// HookId is the id leave and enter receive: the slot header's task id
// (rt.NextTaskId), which no other task of the process ever has.
HookId(t: *Task): u64 {
    unsafe "t is the running task" {
        return (*HeaderOf(t)).id
    }
}

/// HookMute makes the leaves the running task reaches until HookUnmute
/// call no hook, as inside a hook: a wait there parks the task natively
/// and keeps its scheduler's turn (std.core's StreamPut, while it holds a
/// process stream's lock). True when it set the mark, which HookUnmute
/// then clears; false off a task, for a task with no hooks (a native one,
/// whose leaves call none but its spawner notes, which stay due), or
/// inside a hook. O(1); allocates nothing.
export HookMute(): bool {
    t := Current()
    if raw.IsNull(t) {
        return false
    }
    unsafe "t is the running task: its flags are its own" {
        if ((*t).flags & (HookedBit | HookBit)) != HookedBit {
            return false
        }
        (*t).flags = (*t).flags | HookBit
    }
    return true
}

/// HookUnmute ends HookMute's mark when on. O(1); allocates nothing.
export HookUnmute(on: bool) {
    if !on {
        return
    }
    unsafe "the running task's own flags" {
        t := Current()
        (*t).flags = (*t).flags & ~HookBit
    }
}

// HookLeave calls t's leave hook at a leaf's entry; true when it did, and
// the matching HookEnter is owed. A note t owes is sent first, also by a
// native task (outside every hook: HookBit clear). The common case, a
// native task with nothing owed, is one load and two compares.
HookLeave(t: *Task): bool {
    if raw.IsNull(t) {
        return false
    }
    unsafe "t is the running task: its flags and io are its own" {
        var f := (*t).flags
        if (f & (HookedBit | HookBit | NoteBit)) != HookedBit {
            if (f & (NoteBit | HookBit)) == NoteBit {
                HookNote(t)
                f = (*t).flags
            }
            if (f & (HookedBit | HookBit)) != HookedBit {
                return false
            }
        }
        (*t).flags = f | HookBit
        core.IoLeave((*t).io, HookId(t))
    }
    return true
}

// HookEnter calls t's enter hook at a leaf's exit when HookLeave owed it,
// then clears HookBit.
HookEnter(t: *Task, owed: bool) {
    if !owed {
        return
    }
    unsafe "t is the running task" {
        core.IoEnter((*t).io, HookId(t))
        (*t).flags = (*t).flags & ~HookBit
    }
}

// HookFirst is the enter before the task's function first runs.
HookFirst(t: *Task) {
    if HookOwed(t) {
        HookEnter(t, true)
    }
}

// HookLast is the leave after the task's function returned, with LastBit;
// a note the task still owes is sent first (it spawned into
// a scope it does not join and returned with no leaf since). HookBit stays
// set, so nothing the task does on its way out calls a hook.
HookLast(t: *Task) {
    unsafe "t is the running task" {
        if !raw.IsNull(t) && ((*t).flags & (NoteBit | HookBit)) == NoteBit {
            HookNote(t)
        }
    }
    if HookOwed(t) {
        unsafe "t is the running task" {
            core.IoLeave((*t).io, HookId(t) | LastBit)
        }
    }
}
