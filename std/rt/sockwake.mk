// Why: a local Shutdown on Windows must end a receive or a send parked in
// another task at once, as it does on Linux, where shutdown(2) wakes a
// blocked recv with the end of the stream and a blocked send with EPIPE;
// the Windows port completes neither.
//
// What: SockPark, SockUnpark, ShutWake, Parked, ShutRead and ShutWrite.
//
// How: std.task's Transfer links each receive and send it submits on a
// socket into the socket's record (SockPark) and takes it out after its
// completion (SockUnpark), and SockShutdown's job cancels those that its
// direction ends (ShutWake); the caller then reads the cancel's 995 by the
// shut directions SockUnpark answers.

import std.raw
import std.sys

/// ShutRead and ShutWrite are HandleRec.shut's bits: the directions a
/// Shutdown of the socket shut.
export const ShutRead: u32 = 1
export const ShutWrite: u32 = 2

/// Parked is a receive or send submitted to the Windows port on a socket,
/// in its caller's frame from SockPark to SockUnpark.
export struct Parked {
    /// The submitted op (its kind sys.OpRecv or sys.OpSend).
    export op: *sys.Op
    /// The next parked op of the socket, or null.
    export next: *Parked
}

/// SockPark links n, whose op is about to be submitted on the socket h,
/// into h's record, so a Shutdown of h can cancel it. n stays in the
/// caller's frame until its SockUnpark. O(1) under the record's spin lock;
/// allocates nothing.
export SockPark(h: uint, n: *Parked) {
    unsafe "h is a live socket record; n is the caller's, live until SockUnpark" {
        r := Rec(h)
        Lock(&(*r).wake)
        (*n).next = (*r).parked
        (*r).parked = n
        Unlock(&(*r).wake)
    }
}

/// SockUnpark takes n out of h's record after its op completed, and
/// answers the directions a Shutdown of h has shut (ShutRead, ShutWrite).
/// O(p) under the record's spin lock, p the ops parked on h (one a
/// direction in practice); allocates nothing.
export SockUnpark(h: uint, n: *Parked): u32 {
    unsafe "h is a live socket record; n was linked by SockPark" {
        r := Rec(h)
        Lock(&(*r).wake)
        var prev := raw.Null[Parked]()
        var at := (*r).parked
        for !raw.IsNull(at) {
            if raw.Same(at, n) {
                if raw.IsNull(prev) {
                    (*r).parked = (*at).next
                } else {
                    (*prev).next = (*at).next
                }
                break
            }
            prev = at
            at = (*at).next
        }
        shut := (*r).shut
        Unlock(&(*r).wake)
        return shut
    }
}

// ShutWake records that a Shutdown of h by how (0 read, 1 write, 2 both)
// succeeded and cancels the parked ops it ends: the receives for a read
// shutdown, the sends for a write one. A cancel is harmless when its op
// has completed (PollCancel), and the op's frame lives while it is
// linked, which its SockUnpark ends under the same lock.
ShutWake(h: uint, how: u32) {
    bits: u32 = if how == 0 { ShutRead } else if how == 1 { ShutWrite } else { ShutRead | ShutWrite }
    unsafe "h is a live socket record; each linked Parked and its op live until unlinked under wake" {
        r := Rec(h)
        Lock(&(*r).wake)
        (*r).shut = (*r).shut | bits
        var at := (*r).parked
        for !raw.IsNull(at) {
            k := (*(*at).op).kind
            if k == sys.OpRecv && bits & ShutRead != 0 || k == sys.OpSend && bits & ShutWrite != 0 {
                sys.PollCancel((*at).op)
            }
            at = (*at).next
        }
        Unlock(&(*r).wake)
    }
}
