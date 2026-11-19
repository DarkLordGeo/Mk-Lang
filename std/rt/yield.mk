// Why: a spinner whose lock holder lost its CPU should give the CPU away,
// and std.atomic's Spin (through std.core's SpinLock) cannot import std.rt
// to ask for that.
//
// What: MkThreadYield, the hosted yield hook, and LinkYieldHook.
//
// How: Spin calls MkThreadYield after 64 tries, by name with no import. The
// support pack defines a weak default that only pauses
// (internal/codegen/support.go, yieldHook); this strong definition replaces
// it at link time on every hosted target: SwitchToThread on Windows,
// sched_yield on Linux (sys.ThreadYield). It never suspends a task: the
// thread stays the spinning task's.

import std.sys

/// The OS's yield: gives the CPU to another ready thread, if there is one.
/// O(1): one system call; allocates nothing.
export extern MkThreadYield() {
    sys.ThreadYield()
}

/// LinkYieldHook keeps MkThreadYield in every hosted build: std.task.Start,
/// a build root, calls it once, and the fn value it names makes the hook
/// reachable, so its strong definition is emitted and linked over the
/// support pack's weak one (a std module's export extern is no build root
/// of its own). O(1); allocates nothing and calls nothing.
export LinkYieldHook() {
    f := MkThreadYield
    _ = f
}
