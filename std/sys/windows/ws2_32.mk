// Why: std.task's NativeTable names every Io entry, so every program
// reaches the socket calls, and a static ws2_32 import would put WS2_32.dll
// in every executable, even one that never opens a socket.
//
// What: the Winsock entry points std.sys uses, WinsockLoad, the
// skip-on-success check, NewSocket and NewDgram, and TestSkipOff.
//
// How: the entries are loaded from ws2_32.dll on the first socket call
// with LoadLibraryExW and GetProcAddress, as ProcessPrng is. The table
// lives in one committed page made by the thread that loads it; other
// threads read it after seeing state Ready.

import std.raw

struct WinsockState {
    // 0 not loaded, 1 loading (one thread, the others yield), 2 ready,
    // 3 unusable. The loader's Store(2) follows every write to the table
    // page and is sequentially consistent, and readers Load the state
    // before they read the page: that Store/Load pair is the publication
    // edge for the whole table.
    state: raw.Atomic[u32]
    page: raw.Atomic[u64]
    // noskip: 1 when a test asked for sockets without skip-on-success
    // (TestSkipOff), read once by the loader.
    noskip: raw.Atomic[u32]
}

var Winsock: WinsockState = WinsockState(state: raw.Atomic[u32].New(0), page: raw.Atomic[u64].New(0),
    noskip: raw.Atomic[u32].New(0))

/// TestSkipOff is a test switch: every socket this process opens posts a
/// completion packet for a synchronous success too, as on a machine with a
/// layered provider that is not IFS, so the paths that wait for that packet
/// run. It works only before Winsock loads (the first
/// socket): true when it took effect. Edge: the noskip Store and the state
/// Load here, and the loader's state Cas and noskip Load, are sequentially
/// consistent, so a state of 0 seen here means the loader reads noskip 1.
/// O(1); allocates nothing.
export TestSkipOff(): bool {
    Winsock.noskip.Store(1)
    return Winsock.state.Load() == 0
}

// Table slots (8 bytes each) of the entry addresses.
const WsStartup: int = 0
const WsSocket: int = 1
const WsBind: int = 2
const WsListen: int = 3
const WsCloseSocket: int = 4
const WsShutdown: int = 5
const WsGetSockName: int = 6
const WsSetSockOpt: int = 7
const WsIoctl: int = 8
const WsRecv: int = 9
const WsSend: int = 10
const WsLastError: int = 11
const WsOverlapped: int = 12
const WsEnumProtocols: int = 13
const WsAcceptEx: int = 14
const WsConnectEx: int = 15
const WsAcceptAddrs: int = 16
// Slot 17 is 1 when every installed provider gives IFS handles, so
// skip-on-success is safe on sockets (the check Go's runtime makes).
const WsSkipSockets: int = 17
// GetAddrInfoW and FreeAddrInfoW, for ResolveHost (net.mk).
const WsGetAddrInfo: int = 18
const WsFreeAddrInfo: int = 19
// WSARecvFrom and WSASendTo, for the datagram kinds.
const WsRecvFrom: int = 20
const WsSendTo: int = 21
// GetAddrInfoExW in its asynchronous form, its cancel and its free, for the
// lookups on the port (OpResolve, poll_iocp.mk).
const WsGetAddrInfoEx: int = 22
const WsGetAddrInfoExCancel: int = 23
const WsFreeAddrInfoEx: int = 24
// getpeername, for SocketPeer (call.mk).
const WsGetPeerName: int = 25
// connect, for UnixConnect (unix.mk): an AF_UNIX connect
// never waits (a full backlog is WSAENOBUFS at once), and ConnectEx needs a
// bound socket, which a Unix socket has no wildcard for.
const WsConnect: int = 26
// getsockopt, for SocketOptionGet (call.mk).
const WsGetSockOpt: int = 27

const InvalidSocket: uint = 0xFFFF_FFFF_FFFF_FFFF
const WsaIoPending: u32 = 997
const SolSocket: i32 = 0xFFFF
const SoUpdateAcceptContext: i32 = 0x700B
const SoUpdateConnectContext: i32 = 0x7010
// SO_EXCLUSIVEADDRUSE, ~SO_REUSEADDR (winsock2.h).
const SoExclusiveAddrUse: i32 = -5
const SockStream: i32 = 1
const SockDgram: i32 = 2
const IpProtoUdp: i32 = 17
const IpProtoTcp: i32 = 6
const WsaFlagOverlapped: u32 = 0x01
const WsaFlagNoHandleInherit: u32 = 0x80
const SioGetExtensionFunctionPointer: u32 = 0xC800_0006
const Xp1IfsHandles: u32 = 0x2_0000
const WsaProtocolInfoBytes: int = 628

// WsPage is the table, null before it is ready.
WsPage(): *u8 {
    unsafe "the table page, published by the state store" {
        // Edge: the page's Store, before the state's Store of 2 that
        // WinsockReady reads.
        return raw.FromAddr[u8](Trunc[uint](Winsock.page.Load()))
    }
}

// WsFn is slot i of the ready table, for a typed read of the entry.
WsFn(i: int): *u64 {
    unsafe "slot i of the ready table" {
        return raw.PtrCast[u8, u64](raw.Offset(WsPage(), i * 8))
    }
}

// WinsockReady loads Winsock once; false when it is unusable. A thread that
// finds another loading yields until it finishes (a load is a few DLL
// calls, once per process).
WinsockReady(): bool {
    for {
        // Edge: the Cas from 0 admits one loader; its Stores of the page and
        // then 2 publish the table; a Load of 2 sees the page.
        s := Winsock.state.Load()
        if s == 2 {
            return true
        }
        if s == 3 {
            return false
        }
        // Edge: the Cas from 0 admits one loader.
        if s == 0 && Winsock.state.Cas(0, 1) {
            p := WinsockLoad()
            if raw.IsNull(p) {
                Winsock.state.Store(3)
                return false
            }
            // Edge: the page is stored before the state's 2, which publishes
            // it.
            Winsock.page.Store(u64(raw.Addr(p)))
            Winsock.state.Store(2)
            return true
        }
        ThreadYield()
    }
}

// Resolve stores GetProcAddress(m, name) in slot i of p; false if missing.
Resolve(m: uint, p: *u8, i: int, name: str): bool {
    unsafe "kernel32; name is a NUL-terminated constant, p the table page" {
        f := GetProcAddress(m, raw.StrData(name))
        if raw.IsNull(f) {
            return false
        }
        Put64(p, i * 8, u64(raw.Addr(f)))
        return true
    }
}

// Guid writes a GUID (Data1, Data2, Data3 little-endian, Data4 as eight
// bytes, most significant first) at p.
Guid(p: *u8, d1: u32, d2: u16, d3: u16, d4: u64) {
    Put32(p, 0, d1)
    Put16(p, 4, d2)
    Put16(p, 6, d3)
    for i in 0..8 {
        unsafe "p holds 16 bytes" {
            raw.Write(raw.Offset(p, 8 + i), Trunc[u8](d4 >> Trunc[u64](56 - i * 8)))
        }
    }
}

// Extension finds one Mswsock extension function through WSAIoctl on s and
// stores it in slot i of p.
Extension(p: *u8, s: uint, i: int, d1: u32, d2: u16, d3: u16, d4: u64): bool {
    var guid: [2]u64 = [0; 2]
    var f: u64 = 0
    var got: u32 = 0
    unsafe "ws2_32 WSAIoctl; guid, f and got are locals" {
        Guid(raw.PtrCast[u64, u8](raw.DataMut(mut guid[..])), d1, d2, d3, d4)
        ioctl := raw.Read(raw.PtrCast[u8, fn(uint, u32, *u8, u32, *u8, u32, *u32, *u8, *u8): i32](
            raw.Offset(p, WsIoctl * 8)))
        if ioctl(s, SioGetExtensionFunctionPointer, raw.PtrCast[u64, u8](raw.DataMut(mut guid[..])), 16,
            raw.PtrCast[u64, u8](raw.OfMut(mut f)), 8, raw.OfMut(mut got), raw.Null[u8](), raw.Null[u8]()) != 0 {
            return false
        }
    }
    if f == 0 {
        return false
    }
    Put64(p, i * 8, f)
    return true
}

// WinsockLoad runs once: the DLL, its entries, WSAStartup(2.2), the three
// extension functions, and the IFS check of every provider. It returns the
// table page, or null with nothing left mapped. The DLL stays loaded and
// WSAStartup is never undone: sockets live until the process exits.
WinsockLoad(): *u8 {
    p := Reserve(4096, 4096)
    if raw.IsNull(p) {
        return p
    }
    if !Commit(p, 4096) || !WinsockFill(p) {
        Release(p, 4096)
        return raw.Null[u8]()
    }
    return p
}

WinsockFill(p: *u8): bool {
    var name: [16]u16 = [0; 16]
    _ = Wide("ws2_32.dll", mut name[..])
    unsafe "kernel32 and the ws2_32 entries just resolved" {
        m := LoadLibraryExW(raw.Data(name[..]), 0, 0x800)
        if m == 0 {
            return false
        }
        if !(Resolve(m, p, WsStartup, "WSAStartup\u{0}") && Resolve(m, p, WsSocket, "WSASocketW\u{0}")
            && Resolve(m, p, WsBind, "bind\u{0}") && Resolve(m, p, WsListen, "listen\u{0}")
            && Resolve(m, p, WsCloseSocket, "closesocket\u{0}") && Resolve(m, p, WsShutdown, "shutdown\u{0}")
            && Resolve(m, p, WsGetSockName, "getsockname\u{0}") && Resolve(m, p, WsSetSockOpt, "setsockopt\u{0}")
            && Resolve(m, p, WsIoctl, "WSAIoctl\u{0}") && Resolve(m, p, WsRecv, "WSARecv\u{0}")
            && Resolve(m, p, WsSend, "WSASend\u{0}") && Resolve(m, p, WsLastError, "WSAGetLastError\u{0}")
            && Resolve(m, p, WsOverlapped, "WSAGetOverlappedResult\u{0}")
            && Resolve(m, p, WsEnumProtocols, "WSAEnumProtocolsW\u{0}")
            && Resolve(m, p, WsGetAddrInfo, "GetAddrInfoW\u{0}") && Resolve(m, p, WsFreeAddrInfo, "FreeAddrInfoW\u{0}")
            && Resolve(m, p, WsRecvFrom, "WSARecvFrom\u{0}") && Resolve(m, p, WsSendTo, "WSASendTo\u{0}")
            && Resolve(m, p, WsGetAddrInfoEx, "GetAddrInfoExW\u{0}")
            && Resolve(m, p, WsGetAddrInfoExCancel, "GetAddrInfoExCancel\u{0}")
            && Resolve(m, p, WsFreeAddrInfoEx, "FreeAddrInfoExW\u{0}")
            && Resolve(m, p, WsGetPeerName, "getpeername\u{0}") && Resolve(m, p, WsConnect, "connect\u{0}")
            && Resolve(m, p, WsGetSockOpt, "getsockopt\u{0}")) {
            return false
        }
        // WSADATA is 408 bytes on x64.
        var data: [52]u64 = [0; 52]
        startup := raw.Read(raw.PtrCast[u8, fn(u16, *u8): i32](raw.Offset(p, WsStartup * 8)))
        if startup(0x0202, raw.PtrCast[u64, u8](raw.DataMut(mut data[..]))) != 0 {
            return false
        }
        // The extension functions belong to the provider; any TCP socket
        // finds the Microsoft ones every TCP socket of the process uses.
        sock := raw.Read(raw.PtrCast[u8, fn(i32, i32, i32, *u8, u32, u32): uint](raw.Offset(p, WsSocket * 8)))
        s := sock(i32(AfInet), SockStream, IpProtoTcp, raw.Null[u8](), 0, WsaFlagOverlapped | WsaFlagNoHandleInherit)
        if s == InvalidSocket {
            return false
        }
        ok := (Extension(p, s, WsAcceptEx, 0xB536_7DF1, 0xCBAC, 0x11CF, 0x95CA_0080_5F48_A192)
            && Extension(p, s, WsConnectEx, 0x25A2_07B9, 0xDDF3, 0x4660, 0x8EE9_76E5_8C74_063E)
            && Extension(p, s, WsAcceptAddrs, 0xB536_7DF2, 0xCBAC, 0x11CF, 0x95CA_0080_5F48_A192))
        closer := raw.Read(raw.PtrCast[u8, fn(uint): i32](raw.Offset(p, WsCloseSocket * 8)))
        _ = closer(s)
        if !ok {
            return false
        }
    }
    // Edge: the test's Store of noskip, made before the socket is created.
    if AllProvidersIfs(p) && Winsock.noskip.Load() == 0 {
        Put64(p, WsSkipSockets * 8, 1)
    }
    return true
}

// AllProvidersIfs reports whether every installed provider returns IFS
// handles, the only case in which skip-on-success is safe on sockets. A layered provider without them
// still posts a completion for a synchronous success, and the op would be
// delivered after its task had already used the result and left the frame
// (a use after free). The buffer is pages of its own, released here.
AllProvidersIfs(p: *u8): bool {
    var need: u32 = 0
    unsafe "ws2_32 WSAEnumProtocolsW; a first call with no buffer reports the size" {
        protocols := raw.Read(raw.PtrCast[u8, fn(*i32, *u8, *u32): i32](raw.Offset(p, WsEnumProtocols * 8)))
        _ = protocols(raw.Null[i32](), raw.Null[u8](), raw.OfMut(mut need))
        if need == 0 {
            return false
        }
        size := (int(need) + 4095) & ~4095
        b := Reserve(size, 4096)
        if raw.IsNull(b) {
            return false
        }
        if !Commit(b, size) {
            Release(b, size)
            return false
        }
        n := protocols(raw.Null[i32](), b, raw.OfMut(mut need))
        var ifs := n > 0 && i64(n) * i64(WsaProtocolInfoBytes) <= i64(size)
        if ifs {
            for i in 0..int(n) {
                if (Get32(b, i * WsaProtocolInfoBytes) & Xp1IfsHandles) == 0 {
                    ifs = false
                }
            }
        }
        Release(b, size)
        return ifs
    }
}

// SkipSockets reports the IFS check's result.
SkipSockets(): bool {
    return Get64(WsPage(), WsSkipSockets * 8) == 1
}

// WsaError is WSAGetLastError on the calling thread.
WsaError(): u32 {
    unsafe "the table is ready, so slot WSAGetLastError holds WSAGetLastError's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(): i32](WsFn(WsLastError)))
        return Trunc[u32](i64(f()))
    }
}

// WsaFail is the OsResult of the calling thread's last Winsock error.
WsaFail(): OsResult {
    return Fail(WsaError())
}

CloseSocketRaw(s: uint): i32 {
    unsafe "the table is ready, so slot closesocket holds closesocket's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint): i32](WsFn(WsCloseSocket)))
        return f(s)
    }
}

SetSockOpt(s: uint, level: i32, name: i32, val: *u8, len: i32): i32 {
    unsafe "the table is ready, so slot setsockopt holds setsockopt's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, i32, i32, *u8, i32): i32](WsFn(WsSetSockOpt)))
        return f(s, level, name, val, len)
    }
}

BindRaw(s: uint, sa: *u8, len: i32): i32 {
    unsafe "the table is ready, so slot bind holds bind's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, *u8, i32): i32](WsFn(WsBind)))
        return f(s, sa, len)
    }
}

ListenRaw(s: uint, backlog: i32): i32 {
    unsafe "the table is ready, so slot listen holds listen's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, i32): i32](WsFn(WsListen)))
        return f(s, backlog)
    }
}

ShutdownRaw(s: uint, how: i32): i32 {
    unsafe "the table is ready, so slot shutdown holds shutdown's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, i32): i32](WsFn(WsShutdown)))
        return f(s, how)
    }
}

GetSockNameRaw(s: uint, sa: *u8, len: *i32): i32 {
    unsafe "the table is ready, so slot getsockname holds getsockname's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, *u8, *i32): i32](WsFn(WsGetSockName)))
        return f(s, sa, len)
    }
}

ConnectRaw(s: uint, sa: *u8, len: i32): i32 {
    unsafe "the table is ready, so slot connect holds connect's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, *u8, i32): i32](WsFn(WsConnect)))
        return f(s, sa, len)
    }
}

GetSockOpt(s: uint, level: i32, name: i32, val: *u8, len: *i32): i32 {
    unsafe "the table is ready, so slot getsockopt holds getsockopt's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, i32, i32, *u8, *i32): i32](WsFn(WsGetSockOpt)))
        return f(s, level, name, val, len)
    }
}

GetPeerNameRaw(s: uint, sa: *u8, len: *i32): i32 {
    unsafe "the table is ready, so slot getpeername holds getpeername's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(uint, *u8, *i32): i32](WsFn(WsGetPeerName)))
        return f(s, sa, len)
    }
}

// NewSocket is an overlapped, non-inheritable TCP socket of the OS family
// af, or InvalidSocket.
NewSocket(af: u16): uint {
    unsafe "the table is ready, so slot WSASocketW holds WSASocketW's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(i32, i32, i32, *u8, u32, u32): uint](WsFn(WsSocket)))
        // AF_UNIX takes protocol 0 (its provider has no IPPROTO_TCP).
        proto := if af == AfUnixOs { 0 } else { IpProtoTcp }
        return f(i32(af), SockStream, proto, raw.Null[u8](), 0, WsaFlagOverlapped | WsaFlagNoHandleInherit)
    }
}

// NewDgram is an overlapped, non-inheritable UDP socket of the OS family
// af, or InvalidSocket.
NewDgram(af: u16): uint {
    unsafe "the table is ready, so slot WSASocketW holds WSASocketW's address, called with its own signature" {
        f := raw.Read(raw.PtrCast[u64, fn(i32, i32, i32, *u8, u32, u32): uint](WsFn(WsSocket)))
        return f(i32(af), SockDgram, IpProtoUdp, raw.Null[u8](), 0, WsaFlagOverlapped | WsaFlagNoHandleInherit)
    }
}

// OsFamily is the OS family of a SockAddr family, or 0.
OsFamily(f: u16): u16 {
    if f == Inet4 {
        return AfInet
    }
    if f == Unix {
        return AfUnixOs
    }
    return if f == Inet6 { AfInet6 } else { 0 }
}
