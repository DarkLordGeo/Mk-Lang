// linux.Syscall gives the kernel's result as it came back and, for -4095 to -1, errno as its
// negation: write(2) of a message to standard output returns its length with errno 0,
// getpid(2) is positive, close(2) of a bad descriptor is EBADF (9) with r -9's bits, and an
// unknown call number is ENOSYS (38). runtime.Blocking runs a raw call off the carriers, as
// std.linux's doc advises for one that blocks. Expected output: linuxsyscall.out.
import std.linux
import std.raw
import std.runtime

const Msg: str = "written by write(2) through linux.Syscall\n"

// Pid is getpid(2), run on the blocking pool; state is unused.
Pid(state: int): int {
    unsafe "getpid takes no argument and touches no memory" {
        v := linux.Syscall(39, 0, 0, 0, 0, 0, 0)
        if v.errno != 0 {
            return -1
        }
        return Cast[int](v.r)
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    unsafe "each call's arguments are what it takes: Msg's bytes live for the program" {
        w := linux.Syscall(1, 1, raw.Addr(raw.StrData(Msg)), Trunc[uint](Msg.Len()), 0, 0, 0)
        try Print(mut out, "write: r {r} (the message's length {n}), errno {e}\n", (r: w.r, n: Msg.Len(), e: w.errno))
        p := linux.Syscall(39, 0, 0, 0, 0, 0, 0)
        try Print(mut out, "getpid: positive {p}, errno {e}\n", (p: p.r > 0, e: p.errno))
        c := linux.Syscall(3, 0xFFFF_FFFF, 0, 0, 0, 0, 0)
        try Print(mut out, "close of a bad fd: errno {e}, r is -9's bits {r}\n", (e: c.errno, r: c.r == 0xFFFF_FFFF_FFFF_FFF7))
        u := linux.Syscall(100000, 0, 0, 0, 0, 0, 0)
        try Print(mut out, "an unknown call: errno {e}\n", (e: u.errno))
    }
    b := runtime.Blocking(sys.io, 0, Pid) else {
        try Print(mut out, "Blocking: canceled\n")
        return
    }
    try Print(mut out, "getpid on the blocking pool: positive {p}\n", (p: b > 0))
}
