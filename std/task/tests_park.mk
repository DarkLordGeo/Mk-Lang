// Why: a test that cancels a blocked read must know the read reached the
// poller; waiting 1 ms of wall time is not enough on a loaded machine.
// What: OpsInPoller, a probe for test blocks outside std.task (std.net's Read
// and Accept cancel blocks wait on it).
// How: it counts, under the scope's lock, the blocked tasks whose poller
// operation is live (op_live). It changes nothing it reads and adds nothing
// to Await's path.

import std.raw
import std.rt

/// OpsInPoller is the number of tasks blocked in s's own record (not in a
/// nested scope) whose poller operation was submitted and whose task has
/// not yet resumed from the wait: op_live is 1 from PollSubmit's true
/// until then, so an operation that completed but whose task has not run
/// yet is counted too, and so is any blocked operation of s, a dial's
/// included. A cancel of s that starts after this returned 1 finds that
/// operation live, so the cancel walk has issued PollCancel for it before
/// Cancel returns, unless the operation completed first. Tests only. O(n) for the n tasks blocked in
/// s, under s's lock; allocates nothing.
export OpsInPoller(s: &Scope): int {
    r := RecOf(s)
    var n := 0
    unsafe "s's blocked list, under its lock: no task on it can unlink itself" {
        rt.Lock(&(*r).lock)
        var t := (*r).blocked
        for !raw.IsNull(t) {
            // Edge: AwaitIn's op_live.Store(1), made after PollSubmit returned
            // true; CancelWait's cancel.Store(1) and op_live.Load() of a later
            // walk follow this load in S, so that walk reads 1 too.
            if (*t).wait_kind == WaitKindOp && !raw.IsNull((*t).op) && (*t).op_live.Load() == 1 {
                n += 1
            }
            t = (*t).blocked_next
        }
        rt.Unlock(&(*r).lock)
    }
    return n
}
