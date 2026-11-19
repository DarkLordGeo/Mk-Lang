// Why: a Mk function called from C runs as a new task and needs a way to
// get its Io.
// What: runtime.TaskIo.
// How: returns std.task's current Io, or none off a task.

import std.task

/// The running task's Io, or none off a task. A function called from C
/// runs as a new task of its runtime, so TaskIo gives it the native Io,
/// with which it may wait, sleep and do I/O. O(1); allocates nothing.
export TaskIo(): ?Io {
    return task.CurrentIo()
}
