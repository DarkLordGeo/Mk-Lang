// Why: the methods a program calls on its Io (open a scope, sleep, read
// randomness, yield) are the public face of the OS layer and need bodies that
// call the runtime's table.
//
// What: Io.Scope, Io.ScopeOf, Io.ScopeWith, Scope.Cancel, CancelAfter,
// Expired and Remaining (intrinsics), and Io.NoCancel, Io.Sleep, Io.Random,
// Io.Yield and HashKey.New.
//
// How: Scope and Cancel are lowered with the scope construct, their record in
// place in the caller's frame; CancelAfter, Expired and Remaining are lowered
// to std.task's entries of the same names. The bodies here make one table
// call each and read every failure as Canceled, the fail-closed answer.

/// Opens a task scope (see Scope). Its record lives in the caller's frame.
/// O(1); it allocates nothing.
export Io.Scope(self): Scope
/// Opens a task scope whose tasks may fail with E. O(1); it allocates
/// nothing, as Scope.
export Io.ScopeOf[E](self): Scope[E]
/// Opens a task scope under the limits l. O(1); it allocates nothing, as
/// Scope.
export Io.ScopeWith(self, l: Limits): Scope
/// Cancels the scope: each cancellable operation in it, and in the scopes
/// nested in it, fails with Canceled (a Reader or Writer reports ReadError
/// or WriteError). O(scopes nested in it plus the tasks blocked in them):
/// one walk, taking each scope's lock.
export Scope.Cancel(&self)
/// Cancels the scope once d has passed, as Cancel does: every cancellable
/// operation under it, in the body, its tasks and its nested scopes, fails
/// with Canceled. Only the earliest deadline counts, and a zero or negative
/// d cancels at once. The scope owns the timer, and its exit disarms it at
/// once. Under Scope[E] a deadline adds no error. The deadline runs on the
/// clock of the scope's Io. O(log n) in the timers armed; it allocates
/// nothing past the growth of the runtime's timer heap.
///
/// A timeout on one operation is a scope around it: no operation takes a
/// timeout argument, no Scope function takes a time.Instant, and an Io
/// holds no deadline. An operation is cancelled through the scopes of the
/// task that runs it, whichever Io it was given. For a deadline at an
/// instant t, call s.CancelAfter(t.Since(time.Now(io))).
export Scope.CancelAfter(&self, d: Duration)
/// Reports whether this scope's own deadline fired while it was not yet
/// cancelled: true tells a timeout from a Cancel. Once true it stays true
/// until the scope's block exits. A scope cancelled first, by Cancel or
/// through an ancestor, disarms its timer and never expires. O(1).
///
/// To see a deadline after a join, nest two scopes: the deadline on the
/// outer one, the tasks in the inner one, and Expired read after the inner
/// block. Under ScopeOf[E], a task given &s maps its Canceled to
/// time.TimedOut when s.Expired().
export Scope.Expired(&self): bool
/// Returns the least time left over the deadlines of this scope and its
/// ancestors, at least zero, or none when none of them has a deadline.
/// Deadlines above a NoCancel boundary do not count. O(depth of the scope
/// chain).
export Scope.Remaining(&self): ?Duration

/// Returns the same Io with the cancellation in force masked: Cancel, an
/// early exit and the deadlines of the scopes enclosing the call never
/// reach an operation on it. Use it for cleanup that must finish, such as
/// a final write or a close.
///
/// A scope opened from the masked Io, here or later, is a new boundary. Its
/// own Cancel and deadline reach the operations in it and in the scopes
/// under it, and Expired tells why.
///
/// An operation on the masked Io is cancelled only by such a boundary
/// (opened from this Io or from one masked later), or by a scope under one.
/// A scope opened below the mask from an unmasked Io never cancels it.
///
/// A scope opened inside a scope of the masked Io, from either Io, belongs
/// to that scope, so the scopes around the NoCancel call do not reach it.
/// An operation on the unmasked Io under no scope of the masked one is
/// still cancelled by them.
///
/// To keep an outer deadline in force, open that scope outside, and give
/// the masked Io only to the operations that must finish.
///
/// On an Io already masked, the result masks every scope either call would
/// mask. O(1): one call that reads the running task's scope depth; it
/// allocates nothing.
export Io.NoCancel(self): Io {
    var d := uint(TaskDepth())
    if d > MaskDepthMax {
        d = MaskDepthMax
    }
    if (self.flags & 1) != 0 {
        old := (self.flags >> MaskDepthShift) & MaskDepthMax
        if old > d {
            d = old
        }
    }
    keep := self.flags & ~(MaskDepthMax << MaskDepthShift)
    return Io(table: self.table, flags: keep | 1 | (d << MaskDepthShift))
}

// Sleep, Random and Yield: every result of their entry other than None is
// Canceled, the one error their signatures name. The native entries return only
// None or Canceled (std/task/io_misc.mk: an unusable OS random source is
// rt.Fatal there), and Canceled on a NoCancel Io only under a cancelled
// scope opened from a masked Io past its mask (task.IsCancelled,
// MaskedPast). Only a table with no OS behind it returns anything
// else (InertIo, rt.DirectTable: Other); for it Canceled is the
// fail-closed answer of these methods, while core's cancellation check
// (CancelPoint) reads only Canceled as a cancellation: the caller
// stops as it would on a cancel, a sleep loop ends instead of spinning,
// and no caller uses a buffer Random did not fill. A panic (as
// proc.WaitSignal's code 8) would make every such Io fatal for a call whose
// signature already has a failure the caller handles.

/// Suspends the task for at least d. Fails with Canceled when the scope is
/// cancelled first. O(1) here: one runtime call; it allocates nothing.
///
/// A zero or negative d returns at once and is still a cancellation point.
/// A sleep ended by a cancel can be repeated: it consumed nothing.
export Io.Sleep(self, d: Duration): Canceled!void {
    if IoSleep(self, d.Nanos()).err != SysErr.None {
        fail Canceled
    }
}

/// Fills buf with bytes from the OS random source. Fails with Canceled when
/// the scope is cancelled, or on an Io with no OS behind it; buf may then
/// hold anything and must not be used. O(buf.Len()); it allocates nothing.
export Io.Random(self, buf: mut []u8): Canceled!void {
    if IoRandom(self, buf).err != SysErr.None {
        fail Canceled
    }
}

/// Lets other tasks run before this one continues. Fails with Canceled
/// when the scope is cancelled. O(1) here: one runtime call; it allocates
/// nothing.
export Io.Yield(self): Canceled!void {
    if IoYield(self).err != SysErr.None {
        fail Canceled
    }
}

/// Makes a random hash key for NewKeyed tables: 16 bytes of Io.Random,
/// read as SipHash's two key words. A map keyed with it hashes with
/// SipHash-1-3, so its order varies from process to process unless io is
/// simulated. Fails with Canceled as Io.Random does, and then makes no key,
/// so a cancelled caller never gets an all-zero key. Compile-time
/// evaluation runs no Io operation, so it never makes a key. O(1): one
/// Io.Random call of 16 bytes; it allocates nothing.
export HashKey.New(io: Io): Canceled!HashKey {
    var b: [16]u8 = [0; 16]
    try io.Random(mut b[..])
    return HashKeyOf(b[..])
}
