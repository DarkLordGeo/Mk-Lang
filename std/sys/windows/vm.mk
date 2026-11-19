// Why: std.vm lets a program change page protections and generate machine
// code at run time, and on Windows that must hold to W^X: no page is ever
// writable and executable at once.
//
// What: VmProtect, VmCode with VmCodeNew, VmCodeCommit and VmCodeFree, and
// VmFlush.
//
// How: protection changes on committed pages (VirtualProtect), a code
// region's two views of one pagefile-backed section (one view read-write,
// one read-execute), and the instruction-cache flush
// (FlushInstructionCache). Each returns GetLastError's code or 0, so the
// caller can make vm.Error.Os.

import std.raw

const VmPageReadOnly: u32 = 0x02
const VmPageExecuteReadWrite: u32 = 0x40
const VmFileMapWrite: u32 = 0x0002
const VmFileMapRead: u32 = 0x0004
const VmFileMapExecute: u32 = 0x0020
// SEC_RESERVE: a section whose pages are reserved, each committed later by
// VirtualAlloc(MEM_COMMIT) through a view (VmCodeCommit).
const VmSecReserve: u32 = 0x0400_0000
// INVALID_HANDLE_VALUE: a section backed by the paging file.
const VmPagefile: uint = 0xFFFF_FFFF_FFFF_FFFF

/// VmProtect sets the protection of the committed pages [p, p + size):
/// prot 0 none, 1 read, 2 read-write (never executable: std.vm's Prot has no
/// such value). 0, or GetLastError's code (ERROR_INVALID_ADDRESS, 487, for
/// pages that are reserved but not committed).
/// O(1): one system call; allocates nothing.
export VmProtect(p: *u8, size: int, prot: u32): i64 {
    pr: u32 = if prot == 2 { PageReadWrite } else if prot == 1 { VmPageReadOnly } else { PageNoAccess }
    var old: u32 = 0
    unsafe "kernel32; [p, p + size) lies in a reservation the caller owns, and old is a local" {
        if VirtualProtect(p, Trunc[uint](size), pr, raw.OfMut(mut old)) == 0 {
            return i64(GetLastError())
        }
        return 0
    }
}

/// VmCode is a code region's two views of one section: w read-write, x
/// read-execute, each size bytes; err GetLastError's code when it could not
/// be made (then both are null).
export struct VmCode {
    export w: *u8
    export x: *u8
    export err: i64
}

/// VmCodeNew makes a code region of size bytes: a pagefile-backed section
/// created PAGE_EXECUTE_READWRITE (the most any view may take), committed
/// whole (commit) or SEC_RESERVE (not commit: every page reserved until
/// VmCodeCommit), mapped once FILE_MAP_WRITE and once FILE_MAP_READ |
/// FILE_MAP_EXECUTE; the section handle is closed, the views keep it. On
/// any failure what was made is undone and err holds the code.
/// O(1): four system calls; commits nothing until written.
export VmCodeNew(size: int, commit: bool): VmCode {
    u := Trunc[u64](size)
    prot: u32 = if commit { VmPageExecuteReadWrite } else { VmPageExecuteReadWrite | VmSecReserve }
    unsafe "kernel32 on a handle this function owns and closes; the views are new and the caller's" {
        h := CreateFileMappingW(VmPagefile, raw.Null[u8](), prot, Trunc[u32](u >> 32), Trunc[u32](u), raw.Null[u16]())
        if h == 0 {
            return VmCode(w: raw.Null[u8](), x: raw.Null[u8](), err: i64(GetLastError()))
        }
        w := MapViewOfFile(h, VmFileMapWrite, 0, 0, Trunc[uint](size))
        if raw.IsNull(w) {
            e := i64(GetLastError())
            _ = CloseHandle(h)
            return VmCode(w: raw.Null[u8](), x: raw.Null[u8](), err: e)
        }
        x := MapViewOfFile(h, VmFileMapRead | VmFileMapExecute, 0, 0, Trunc[uint](size))
        e := i64(GetLastError())
        _ = CloseHandle(h)
        if raw.IsNull(x) {
            _ = UnmapViewOfFile(w)
            return VmCode(w: raw.Null[u8](), x: raw.Null[u8](), err: e)
        }
        return VmCode(w: w, x: x, err: 0)
    }
}

/// VmCodeCommit commits [offset, offset + size) of a region VmCodeNew made
/// with commit false, page-aligned and inside it: VirtualAlloc(MEM_COMMIT)
/// through the writable view, with prot 1 read-only or 2 read-write, which
/// commits the section's pages, so the executable view sees them
/// PAGE_EXECUTE_READ (probed on Windows 11). 0, or
/// GetLastError's code.
/// O(1): one system call; commits size bytes.
export VmCodeCommit(c: &VmCode, offset: int, size: int, prot: u32): i64 {
    pr: u32 = if prot == 2 { PageReadWrite } else { VmPageReadOnly }
    unsafe "kernel32; [w + offset, w + offset + size) lies in the caller's writable view" {
        if raw.IsNull(VirtualAlloc(raw.FromAddr[u8](raw.Addr(c.w) + Trunc[uint](offset)), Trunc[uint](size), MemCommit, pr)) {
            return i64(GetLastError())
        }
        return 0
    }
}

/// VmCodeFree unmaps both views VmCodeNew made.
/// O(1): two system calls; gives back the region. Allocates nothing.
export VmCodeFree(c: &VmCode, size: int) {
    unsafe "kernel32; both views are the caller's and no longer used" {
        _ = UnmapViewOfFile(c.w)
        _ = UnmapViewOfFile(c.x)
    }
}

/// VmFlush makes code written through a region's writable view visible to
/// execution at [x, x + n): FlushInstructionCache, which Windows documents
/// as required after writing code, though x86_64 keeps the caches coherent.
/// O(1): one system call; allocates nothing.
export VmFlush(x: *u8, n: int) {
    unsafe "kernel32; [x, x + n) is a view the caller owns" {
        _ = FlushInstructionCache(GetCurrentProcess(), x, Trunc[uint](n))
    }
}
