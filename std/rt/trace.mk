// Why: when an error reaches Main, the reader wants to see where it was
// raised and the try sites it passed through, not only its name.
//
// What: the error-return trace: TraceFail, TraceTry, TraceReset,
// WriteTrace, Traced, and the build probe ProbeBuild and DevBuild.
//
// How: dev and test builds record up to 12 rows per task in its slot
// header. Off a task stack it records nothing. Release builds emit no calls
// (mono drops them).

import std.raw

// Tracing is set by the first trace call, which only dev and test builds
// emit; Join's trace reset runs only then, so release adds no row.
struct TraceWords {
    on: raw.Atomic[u32]
}

var Tracing: TraceWords = TraceWords(on: raw.Atomic[u32].New(0))

/// Traced is whether any trace call has run, which only dev and test builds
/// emit.
/// O(1); allocates nothing.
export Traced(): bool {
    // Edge: Tracing.on is written before any task exists (ProbeBuild) or
    // by the task whose trace call it is; a reader on another task that
    // misses it skips only a trace reset for an error it did not see.
    return Tracing.on.Load() != 0
}

// The build mode (MK_TEST_SLOTS and
// MK_TEST_POOL are read only outside release builds). TargetInfo has no
// build-mode field, so the runtime asks the one fact a release build
// changes in std.rt: mono drops every direct call to TraceFail there
// (in release builds). A compile-time build-mode constant would
// replace this.
struct BuildWords {
    dev: raw.Atomic[u32]
}

var Build: BuildWords = BuildWords(dev: raw.Atomic[u32].New(0))

/// ProbeBuild runs once in Start, before any task exists and off every task
/// stack, where TraceFail records no row: in a dev or test build its call
/// runs and sets Tracing; in a release build the call is gone and Tracing
/// stays 0.
/// O(1); allocates nothing.
export ProbeBuild() {
    var site := Site(file: "std/rt/trace.mk", line: 0, col: 0)
    TraceFail(&site)
    if Traced() {
        // Edge: written before any other thread or task exists; every
        // reader runs after Start's later stores publish the TaskState.
        Build.dev.Store(1)
    }
}

/// DevBuild is true in dev and test builds, false in release (ProbeBuild).
/// O(1); allocates nothing.
export DevBuild(): bool {
    // Edge: ProbeBuild's store, before any task.
    return Build.dev.Load() != 0
}

// TraceOn sets Tracing. Edge: see Traced. The Load first: the word is set
// once and never cleared, and a seq_cst store is an xchg on x86_64, which
// took the shared line on every error return of every carrier (dev and
// test builds only: release emits no trace calls).
TraceOn() {
    if Tracing.on.Load() == 0 {
        Tracing.on.Store(1)
    }
}

/// TraceFail starts the running task's trace at site, a `fail` statement's
/// position; off a task stack it records nothing. site must be a static
/// site constant, as codegen passes it. O(1); allocates nothing.
export TraceFail(site: &Site) {
    TraceOn()
    h := CurrentSlot()
    if raw.IsNull(h) {
        return
    }
    unsafe "h is CurrentSlot(), not null: the running task's own header, which only that task writes" {
        (*h).trace_n = 1
        (*h).trace_dropped = 0
        (*h).trace[0] = raw.Of(site)
    }
}

/// TraceTry adds site, a `try` that passed an error on, to the running
/// task's trace; past 12 rows it counts a dropped row instead. O(1);
/// allocates nothing.
export TraceTry(site: &Site) {
    TraceOn()
    h := CurrentSlot()
    if raw.IsNull(h) {
        return
    }
    unsafe "h is CurrentSlot(), not null: the running task's own header, which only that task writes" {
        if (*h).trace_n < 12 {
            (*h).trace[int((*h).trace_n)] = raw.Of(site)
            (*h).trace_n += 1
        } else {
            (*h).trace_dropped += 1
        }
    }
}

/// TraceReset makes the trace the one row site (Join calls it).
/// O(1); allocates nothing.
export TraceReset(h: *SlotHeader, site: *Site) {
    unsafe "the caller passes the running task's own header, which only that task writes" {
        (*h).trace_n = 1
        (*h).trace_dropped = 0
        (*h).trace[0] = site
    }
}

// WriteTrace writes the current task's rows, "    at <file>:<line>:<col>",
// then "    ... <n> more". A row is one write when it fits 512 bytes,
// else written in order in pieces, never truncated (as the panic report).
WriteTrace() {
    h := CurrentSlot()
    if raw.IsNull(h) {
        return
    }
    unsafe "h is the running task's slot header; its rows are site constants" {
        for i in 0..int((*h).trace_n) {
            s := (*h).trace[i]
            var buf: [512]u8 = [0; 512]
            var n := Put(mut buf[..], 0, "    at ")
            n = Put(mut buf[..], n, (*s).file)
            n = Put(mut buf[..], n, ":")
            n = Digits(mut buf[..], n, u64((*s).line))
            n = Put(mut buf[..], n, ":")
            n = Digits(mut buf[..], n, u64((*s).col))
            n = Put(mut buf[..], n, "\n")
            if n <= 512 {
                WriteErr(buf[..n])
            } else {
                var pos: [64]u8 = [0; 64]
                var m := Put(mut pos[..], 0, ":")
                m = Digits(mut pos[..], m, u64((*s).line))
                m = Put(mut pos[..], m, ":")
                m = Digits(mut pos[..], m, u64((*s).col))
                m = Put(mut pos[..], m, "\n")
                WriteErr("    at ".AsBytes())
                WriteErr((*s).file.AsBytes())
                WriteErr(pos[..m])
            }
        }
        if (*h).trace_dropped > 0 {
            var buf: [64]u8 = [0; 64]
            var n := Put(mut buf[..], 0, "    ... ")
            n = Digits(mut buf[..], n, u64((*h).trace_dropped))
            n = Put(mut buf[..], n, " more\n")
            WriteErr(buf[..n])
        }
    }
}
