// Why: std.task stays private; std.runtime is the public door to the
// scheduler, and this file is the small set of facts it reads from it.
//
// What: CarriersInUse, SlotGuarantee, CarrierGuarantee and YieldTask.
//
// How: each reads the TaskState page or a per-OS constant; YieldTask yields
// 16 times, then parks on a short timer that doubles up to 1.28 ms.

import std.raw
import std.rt

/// CarriersInUse is the number of carriers this process runs (MK_CARRIERS,
/// or the processor count); 1 before std.task starts, and in a
/// program with no task, whose Main runs on the main thread alone.
/// O(1); allocates nothing.
export CarriersInUse(): int {
    // Edge: as TS()'s load: TaskInit stores the page address after the
    // page's fields, so a nonzero word comes with want stored.
    if Sched.state.Load() == 0 || !rt.UsesTasks() {
        return 1
    }
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: want is stored before any task runs (CarriersInit).
        return int((*TS()).want.Load())
    }
}

/// SlotGuarantee is the part of a task slot above its guard page that the
/// stack-overflow report may use and a task never does: TaskGuarantee on
/// Windows (GuaranteedStackBytes), 0 on Linux, whose report runs on the
/// carrier's alternate signal stack. O(1). Allocates nothing.
export SlotGuarantee(): uint {
    return if Target.os == Os.Windows { Trunc[uint](TaskGuarantee) } else { 0 }
}

/// CarrierGuarantee is the same for a thread stack sys.ThreadStart made (a
/// carrier's system stack, a pool thread): sys.ThreadInit's 32 KiB on
/// Windows and the guard pages Windows keeps below it, 0 on Linux. O(1).
/// Allocates nothing.
export CarrierGuarantee(): uint {
    return if Target.os == Os.Windows { 32768 + 3 * 4096 } else { 0 }
}

/// YieldTask lets the running task wait its turn for the n-th time
/// (counting from 0) through the scheduler, with backoff: the first 16
/// tries yield it (YieldNow: it goes to the global queue's tail, so every
/// other runnable task goes first), later ones park it on a timer of 10
/// microseconds doubling to 1.28 ms, not cancellable, so the carrier runs
/// other tasks, its timers and its completions meanwhile. True on a task;
/// off a task stack it does nothing and returns false. std.runtime's
/// Foreign.Release waits with it, holding no carrier.
/// O(1) besides the switch; allocates nothing.
export YieldTask(n: int): bool {
    if raw.IsNull(Current()) {
        return false
    }
    if n < 16 {
        YieldNow()
    } else {
        var shift := n - 16
        if shift > 7 {
            shift = 7
        }
        _ = SleepFor(10_000 << Trunc[i64](shift), false)
    }
    return true
}
