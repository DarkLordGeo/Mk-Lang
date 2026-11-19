// Why: an Mk library (a DLL or a static library) runs its own runtime
// inside a host program, and its fault handler must not claim the host's
// faults or outlive the library's stop.
//
// What: LibraryImage, FaultUnhook, LibraryOwnerHook, LibPassOverflow and
// LibraryEnv, with LibFaultState, the library's handler state.
//
// How: a library's image bounds come from its own image (__ImageBase), so
// its vectored handler passes on every fault outside the DLL, a stack
// overflow that is not Mk's included (LibPassOverflow), and its stop
// removes the handler.

import std.raw

// MkImageBase is the address of the library's own image (__ImageBase),
// defined by the support pack of a library build only.
extern MkImageBase(): uint

// LibFaultState is a library's handler state: on is 1 once LibraryImage
// ran; veh is the vectored handler, for the stop (0 outside a library or
// before FaultHook); dll is 1 when the image is the library's own (a DLL),
// 0 when a static library's image is the host executable's; own holds the
// bytes of FaultOwnerHook's fn(uint): bool (whether a stack pointer is on
// a stack Mk made), own_set 1 once it is written.
struct LibFaultState {
    on: raw.Atomic[u32]
    veh: raw.Atomic[u64]
    dll: raw.Atomic[u32]
    own: raw.Atomic[u64]
    own_set: raw.Atomic[u32]
}

var LibFaults: LibFaultState = LibFaultState(on: raw.Atomic[u32].New(0), veh: raw.Atomic[u64].New(0), dll: raw.Atomic[u32].New(0),
    own: raw.Atomic[u64].New(0), own_set: raw.Atomic[u32].New(0))

// ImageFileDll is IMAGE_FILE_DLL, the PE file header's DLL characteristic.
const ImageFileDll: u16 = 0x2000

/// LibraryImage takes the calling library's image bounds from its own PE
/// headers, not the host executable's, for the vectored handler's test,
/// and marks the process's handlers as a library's: FaultHook then keeps
/// the host's unhandled-exception filter. Called once, by
/// std.task.StartLibrary, before FaultHook. O(1); allocates nothing.
export LibraryImage(): (lo: uint, hi: uint) {
    var lo: uint = 0
    var hi: uint = 0
    var dll := false
    unsafe "the image's own PE headers, mapped with it" {
        base := MkImageBase()
        b := raw.FromAddr[u8](base)
        nt := u32(raw.ReadUnaligned(raw.PtrCast[u8, u32](raw.Offset(b, 0x3C))))
        size := raw.ReadUnaligned(raw.PtrCast[u8, u32](raw.Offset(b, int(nt) + 0x50)))
        // The file header's Characteristics, at 4 + 18: a static
        // library's __ImageBase is the host executable's.
        dll = (raw.ReadUnaligned(raw.PtrCast[u8, u16](raw.Offset(b, int(nt) + 0x16))) & ImageFileDll) != 0
        lo = base
        hi = base + uint(size)
    }
    // Edge: stored before FaultHook installs the handler that reads them.
    Faults.image_lo.Store(u64(lo))
    // Edge: as image_lo's.
    Faults.image_hi.Store(u64(hi))
    // Edge: as image_lo's.
    LibFaults.dll.Store(if dll { 1 } else { 0 })
    // Edge: as image_lo's; FaultHook reads it after this store.
    LibFaults.on.Store(1)
    return (lo: lo, hi: hi)
}

/// FaultUnhook removes a library's vectored handler at its stop; outside a
/// library it does nothing. O(1) system calls; allocates nothing.
export FaultUnhook() {
    // Edge: LibraryImage's store, on this process's start path.
    if LibFaults.on.Load() == 0 {
        return
    }
    // Edge: the Swap takes the handler once; FaultHook stored it.
    h := LibFaults.veh.Swap(0)
    if h != 0 {
        unsafe "kernel32; h is the handler AddVectoredExceptionHandler gave" {
            _ = RemoveVectoredExceptionHandler(raw.FromAddr[u8](Trunc[uint](h)))
        }
    }
    // Edge: a later FaultHook installs again.
    Faults.set.Store(0)
}

/// LibraryOwnerHook keeps f, which answers whether a stack pointer lies on
/// a task slot or a carrier's system stack, for LibPassOverflow: a
/// library's test of a stack overflow. Called once, by
/// std.task.StartLibrary, before FaultHook. O(1); allocates nothing.
export LibraryOwnerHook(f: fn(uint): bool) {
    unsafe "the hook word is an atomic's memory" {
        raw.Write(raw.PtrCast[raw.Atomic[u64], fn(uint): bool](raw.Of(&LibFaults.own)), f)
    }
    // Edge: the Store publishes the hook's bytes, written above, to
    // LibPassOverflow's Load (FaultHook installs the handler after it).
    LibFaults.own_set.Store(1)
}

// LibPassOverflow is a library's test for a stack overflow:
// true when it is not Mk's, so the vectored handler passes it on to the
// host's own handlers (ContinueSearch): its pc is outside the library's
// own code (a DLL's image; a static library has no image of its own) and
// its stack pointer is on no stack Mk made (no user extern's carrier
// stack, top 0; no task slot or carrier system stack, the owner hook).
// False outside a library, where every overflow is the program's. It
// runs in the handler, on the overflowing stack: loads and the hook's
// compares, no lock, no allocation.
LibPassOverflow(pc: uint, sp: uint, top: uint): bool {
    // Edge: LibraryImage stored on, dll and the bounds before FaultHook
    // installed the handler.
    if LibFaults.on.Load() == 0 || top != 0 {
        return false
    }
    // Edge: as on's.
    if LibFaults.dll.Load() != 0 && InImage(pc) {
        return false
    }
    // Edge: LibraryOwnerHook's Store, after the hook's bytes.
    if LibFaults.own_set.Load() == 1 {
        unsafe "the hook word holds the fn LibraryOwnerHook wrote before its set word" {
            f := raw.Read(raw.PtrCast[raw.Atomic[u64], fn(uint): bool](raw.Of(&LibFaults.own)))
            if f(sp) {
                return false
            }
        }
    }
    return true
}

/// LibraryEnv has std/sys/linux's contract; here the environment is read
/// from the process (GetEnvironmentStringsW, GetEnvironmentVariableW),
/// never from a start stack, so a library needs none: null. O(1);
/// allocates nothing.
export LibraryEnv(): *u8 {
    return raw.Null[u8]()
}
