// Why: Windows reads a thread's stack bounds from its TEB, so a task that
// runs on its own stack must carry its own NT_TIB words, or exception
// dispatch and stack probes refuse its frames.
// What: Teb, StackWords and ThreadStackBounds.
// How: the TEB is gs:0x30; StackWords reads the four words mk_switch saves
// and installs at each task switch.

import std.raw

// Teb is the intrinsic `movq %gs:0x30, $0`: the current thread's TEB.
Teb(): *u8

/// StackWords are the four NT_TIB words mk_switch saves and installs:
/// StackBase (gs:0x08), StackLimit (gs:0x10), DeallocationStack (gs:0x1478)
/// and GuaranteedStackBytes (gs:0x1748), for the current thread. Linux has
/// none and returns zeros.
/// O(1); allocates nothing.
export StackWords(): [4]u64 {
    var w: [4]u64 = [0; 4]
    unsafe "the TEB of the running thread" {
        t := Teb()
        w[0] = raw.Read(raw.PtrCast[u8, u64](raw.Offset(t, 0x08)))
        w[1] = raw.Read(raw.PtrCast[u8, u64](raw.Offset(t, 0x10)))
        w[2] = raw.Read(raw.PtrCast[u8, u64](raw.Offset(t, 0x1478)))
        w[3] = raw.Read(raw.PtrCast[u8, u64](raw.Offset(t, 0x1748)))
    }
    return w
}

/// ThreadStackBounds is the current thread's own stack, [lo, hi), for the
/// fault filter (std.rt's sys_lo and sys_hi).
/// O(1): one call; allocates nothing.
export ThreadStackBounds(): (lo: uint, hi: uint) {
    var lo: uint = 0
    var hi: uint = 0
    unsafe "kernel32; lo and hi are words in this frame" {
        GetCurrentThreadStackLimits(raw.OfMut(mut lo), raw.OfMut(mut hi))
    }
    return (lo: lo, hi: hi)
}
