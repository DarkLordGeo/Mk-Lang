// Why: the summary printed at shutdown needs what happened to each job.
//
// What: Table (New, Begin, End, Get), JobState, Outcome and FromStatus.
//
// How: the table lives on the allocator the daemon is given, so
// `--max-memory` bounds it.

import std.proc

/// How one run of a job ended.
export enum Outcome {
    /// It exited with a status
    Exited(code: int)
    /// A signal ended it (Linux)
    Signalled
    /// Its timeout ran out, and its group was killed
    TimedOut
    /// The daemon stopped and interrupted it
    Interrupted
    /// The daemon stopped and left it running, as the entry asked
    Detached
    /// It could not be started
    NotStarted
    /// Simulated: the run was recorded, not started
    Recorded
}

/// The counts for one entry.
export struct JobState {
    export started: int
    export ok: int
    export failed: int
    export timed_out: int
    export interrupted: int
    export detached: int
    export overlapped: int
    export running: bool
}

/// One state per entry, in crontab order.
export view linear struct Table {
    jobs: HeapList[JobState]
}

/// A table of `n` entries on `a`. Fails with OutOfMemory when `a` cannot
/// hold it. O(n); allocates n states.
export Table.New(a: &Alloc, n: int): OutOfMemory!Table {
    var jobs := HeapList[JobState].New(a)
    onerr jobs.Free(a)
    for _ in 0..n {
        try jobs.Push(a, JobState(started: 0, ok: 0, failed: 0, timed_out: 0, interrupted: 0, detached: 0, overlapped: 0, running: false))
    }
    return Table(jobs: jobs)
}

/// Marks entry `i` running; false, counted, when its last run is still
/// going, since a job never runs beside itself.
export Table.Begin(mut self, i: int): bool {
    var s := self.jobs.GetMut(i).Must() // i indexes the crontab, which sized the table
    if s.running {
        s.overlapped += 1
        return false
    }
    s.running = true
    s.started += 1
    return true
}

/// Records how a run of entry `i` ended.
export Table.End(mut self, i: int, o: Outcome) {
    var s := self.jobs.GetMut(i).Must() // as in Begin
    s.running = false
    match o {
        .Exited(code) {
            if code == 0 {
                s.ok += 1
            } else {
                s.failed += 1
            }
        }
        .Signalled | .NotStarted { s.failed += 1 }
        .TimedOut { s.timed_out += 1 }
        .Interrupted { s.interrupted += 1 }
        .Detached { s.detached += 1 }
        .Recorded { s.ok += 1 }
    }
}

/// The state of entry `i`.
export Table.Get(&self, i: int): JobState {
    return *self.jobs.Get(i).Must() // as in Begin
}

impl Free for Table {
    Free(self, a: &Alloc) {
        Table(jobs) := self
        jobs.Free(a)
    }
}

/// The outcome a child's status means: an exit code, or a signal.
export FromStatus(st: &proc.Status): Outcome {
    if code := st.code {
        return Outcome.Exited(code: code)
    }
    return Outcome.Signalled
}
