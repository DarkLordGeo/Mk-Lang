// Why: tasks cannot print (a Writer stays on its task), so what each process
// task has to say must travel to the main task.
//
// What: Event, Phase and StopReason.
//
// How: every run, exit and restart is an Event on one channel, received in
// the order the tasks sent them.

import std.proc
import src.rundir

/// One step in the life of a process.
export enum Phase {
    /// A run began; `run` counts from 1
    Started
    /// The run ended by itself with `code`, and `stdout` and `stderr` hold its output
    Exited
    /// The run could not start or overran its limit; `failure` says why
    Failed
    /// Procman will start it again after `delay_ms`
    Restarting
    /// It failed `retries` times more and will not be started again
    GaveUp
    /// Procman stopped it, killing a run in progress
    Stopped
    /// Writing the run directory failed; `dir_failure` says why, and the process goes on
    DirFailed
}

/// An event and the output it carries. Linear: the receiver frees it.
export linear struct Event {
    export proc: int
    export phase: Phase
    export run: int
    export code: int
    export delay_ms: i64
    /// For `Restarting`, which restart in a row this is
    export attempt: int
    export failure: ?proc.Error
    export dir_failure: ?rundir.Error
    export stdout: HeapText
    export stderr: HeapText
}

/// An event with no output.
export Event.New(a: &Alloc, proc: int, phase: Phase, run: int): Event {
    return Event(proc: proc, phase: phase, run: run, code: 0, delay_ms: 0, attempt: 0, failure: none, dir_failure: none, stdout: HeapText.New(a), stderr: HeapText.New(a))
}

impl Free for Event {
    Free(self, a: &Alloc) {
        Event(stdout, stderr, ..) := self
        stdout.Free(a)
        stderr.Free(a)
    }
}

/// Why procman stopped its processes, stored once by whoever stops it first.
export enum StopReason {
    /// Every process ended by itself
    Finished
    /// A process marked `main` ended
    MainEnded
    Interrupt
    Terminate
    Hangup
    /// `--stop-after` ran out
    Timeout
}
