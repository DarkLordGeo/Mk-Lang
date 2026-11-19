// Why: the OS structures std.sys fills and reads (OVERLAPPED, sockaddr,
// io_uring rings and SQEs, epoll_event) have fixed byte layouts that Mk
// structs do not model.
//
// What: Get16, Get32, Get64, Put16, Put32, Put64 and Swap16, OsAddrLen, and
// EncodeAddr and DecodeAddr between SockAddr and the OS's sockaddr.
//
// How: plain reads and writes at byte offsets. Every caller passes a pointer
// to at least off + the field's size bytes, and OS structure fields are
// naturally aligned at the offsets used.

import std.raw

Get16(p: *u8, off: int): u16 {
    unsafe "the caller's structure holds off + 2 bytes" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u16](raw.Offset(p, off)))
    }
}

Get32(p: *u8, off: int): u32 {
    unsafe "the caller's structure holds off + 4 bytes" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u32](raw.Offset(p, off)))
    }
}

Get64(p: *u8, off: int): u64 {
    unsafe "the caller's structure holds off + 8 bytes" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, off)))
    }
}

Put16(p: *u8, off: int, v: u16) {
    unsafe "the caller's structure holds off + 2 bytes" {
        raw.Write(raw.PtrCast[u8, u16](raw.Offset(p, off)), v)
    }
}

Put32(p: *u8, off: int, v: u32) {
    unsafe "the caller's structure holds off + 4 bytes" {
        raw.Write(raw.PtrCast[u8, u32](raw.Offset(p, off)), v)
    }
}

Put64(p: *u8, off: int, v: u64) {
    unsafe "the caller's structure holds off + 8 bytes" {
        raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, off)), v)
    }
}

// Swap16 converts a port between host and network order.
Swap16(v: u16): u16 {
    return (v >> 8) | (v << 8)
}

// OsAddrLen is the size of the OS sockaddr for family (sockaddr_in 16,
// sockaddr_in6 28; the layouts are the same on Windows and Linux), or 0.
OsAddrLen(family: u16): int {
    if family == Inet4 {
        return 16
    }
    return if family == Inet6 { 28 } else { 0 }
}

// EncodeAddr writes a as the OS sockaddr at p (at least 28 bytes) and
// returns its length, or 0 for an unknown family.
EncodeAddr(a: &SockAddr, p: *u8): int {
    n := OsAddrLen(a.family)
    if n == 0 {
        return 0
    }
    unsafe "p holds 28 bytes" {
        raw.Fill(p, 0, 28)
        if a.family == Inet4 {
            Put16(p, 0, AfInet)
            for i in 0..4 {
                raw.Write(raw.Offset(p, 4 + i), a.addr[i])
            }
        } else {
            Put16(p, 0, AfInet6)
            for i in 0..16 {
                raw.Write(raw.Offset(p, 8 + i), a.addr[i])
            }
            Put32(p, 24, a.scope_id)
        }
    }
    Put16(p, 2, Swap16(a.port))
    return n
}

// DecodeAddr reads the OS sockaddr at p into a; false for a family other
// than AF_INET and AF_INET6 (a is then the zero address).
DecodeAddr(p: *u8, a: mut SockAddr): bool {
    a.family = 0
    a.port = 0
    a.addr = [0; 16]
    a.scope_id = 0
    f := Get16(p, 0)
    unsafe "p holds the sockaddr the OS wrote" {
        if f == AfInet {
            a.family = Inet4
            for i in 0..4 {
                a.addr[i] = raw.Read(raw.Offset(p, 4 + i))
            }
        } else if f == AfInet6 {
            a.family = Inet6
            for i in 0..16 {
                a.addr[i] = raw.Read(raw.Offset(p, 8 + i))
            }
            a.scope_id = Get32(p, 24)
        } else {
            return false
        }
    }
    a.port = Swap16(Get16(p, 2))
    return true
}
