# std.linux

`std.linux` makes raw Linux system calls: a call number and six arguments in, the kernel's result and `errno` out, with no C library in between. Reach for it when a Linux program needs a system call that std does not wrap. For files, processes, sockets and time, use `std.fs`, `std.proc`, `std.net` and `std.time`, which also work on Windows and under a simulated `Io`.

Read [unsafe code](../reference.md#14-unsafe-and-c) and [per-target modules](../reference.md#per-target-modules) in the reference first.

```mk
// verdict: parse
import std.linux
```

## Contents

- [The main pieces](#the-main-pieces)
- [Targets](#targets)
- [Results and errors](#results-and-errors)
- [Blocking and cost](#blocking-and-cost)
- [Examples](#examples): [make system calls](#make-system-calls), [turn errno into an error](#turn-errno-into-an-error), [choose a module per target](#choose-a-module-per-target)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Make a system call | `Syscall(nr, a0, a1, a2, a3, a4, a5)` |
| Tell success from failure | the result's `errno`: 0 on success |
| Pass a buffer or a string | `raw.Addr(raw.StrData(s))` or `raw.Addr(raw.Data(b))` from `std.raw` for bytes the kernel reads; `raw.Addr(raw.DataMut(mut b))` for a buffer it writes |

`Syscall` is the only function. It is callable only inside an `unsafe "reason" { }` block (E1101): the meaning of each argument, and whether the call is safe, are the system call's. The module that holds the block is listed in the manifest's `unsafe_modules` (E1102, [grants for unsafe code](../packages.md#11-grants-for-unsafe-code)).

## Targets

`std.linux` exists on `x86_64-linux` only. On any other target, its import is E0202 at the import (`reason: target-selected`), even in code that never runs there.

So a package that also builds for Windows imports `std.linux` only in a module that its manifest's `targets` select for `linux`. It gives the same module name a Windows directory too. The rest of the package imports that module by its one name. `mk check --all-targets` checks that the alternatives export the same API (E0213).

## Results and errors

`Syscall` returns `(r: uint, errno: int)`:

- `r` is the kernel's result as it came back, a `uint`.
- When the result, read as a signed number, is from -4095 to -1, the call failed. Then `errno` is its negation, such as 9 for `EBADF` or 38 for `ENOSYS`, and `r` holds the raw negative value. Otherwise `errno` is 0.

Pass 0 for arguments a call does not use. Call numbers and `errno` values are the kernel's x86_64 ones, from its `asm/unistd_64.h` and `errno.h` headers.

## Blocking and cost

`Syscall` is one `syscall` instruction and nothing more. It does not retry on `EINTR`, and it does not suspend the task. A call that blocks, such as a `read` of a pipe, blocks the carrier thread under the task. Run such a call through `runtime.Blocking`, which takes it off the carriers ([blocking foreign calls](runtime.md#blocking-foreign-calls)). `Syscall` allocates nothing and costs the system call itself.

## Examples

The programs are for `x86_64-linux`: build them with `mk build --target x86_64-linux`, from any host, and run them on Linux. The first two need a Linux machine to run, so no output is shown for them. The last also builds and runs on Windows.

### Make system calls

```mk
// verdict: parse
import std.linux
import std.raw

const SysWrite: uint = 1
const SysGetpid: uint = 39
const SysClose: uint = 3

export Main(sys: &System): WriteError!void {
    msg := "hello from write(2)\n"
    unsafe "write(1, msg, len) reads len bytes of msg and nothing else" {
        r := linux.Syscall(SysWrite, 1, raw.Addr(raw.StrData(msg)), Cast[uint](msg.Len()), 0, 0, 0)
        if r.errno != 0 {
            try sys.Print("write failed: errno {e}\n", (e: r.errno))
        }
    }
    unsafe "getpid takes no arguments and cannot fail" {
        pid := linux.Syscall(SysGetpid, 0, 0, 0, 0, 0, 0)
        try sys.Print("pid {p}\n", (p: pid.r))
    }
    unsafe "close of a descriptor this program never opened touches nothing" {
        bad := linux.Syscall(SysClose, 9999, 0, 0, 0, 0, 0)
        try sys.Print("close(9999): errno {e}\n", (e: bad.errno)) // 9, EBADF
    }
}
```

- Its package lists the module in `unsafe_modules`, for the `unsafe` blocks and for `std.raw`.
- `raw.Addr(raw.StrData(msg))` passes the string's address as the integer the kernel expects.

### Turn errno into an error

```mk
// verdict: parse
import std.linux

// Errno is a failed system call, with the kernel's error number.
error Errno(code: int)

// Dup duplicates the file descriptor fd with dup(2), and returns the new one.
Dup(fd: uint): Errno!uint {
    unsafe "dup reads only its argument, a descriptor number" {
        r := linux.Syscall(32, fd, 0, 0, 0, 0, 0)
        if r.errno != 0 {
            fail Errno(code: r.errno)
        }
        return r.r
    }
}

// Close closes fd with close(2).
Close(fd: uint): Errno!void {
    unsafe "close ends only the descriptor fd, which this program owns" {
        r := linux.Syscall(3, fd, 0, 0, 0, 0, 0)
        if r.errno != 0 {
            fail Errno(code: r.errno)
        }
    }
}

export Main(sys: &System): Errno|WriteError!void {
    fd := try Dup(1) // a second descriptor for standard output
    try sys.Print("dup(1) = {fd}\n", (fd))
    try Close(fd)
    Close(fd) else e { // closed already
        match e {
            Errno(code) { try sys.Print("second close: errno {code}\n", (code)) }
        }
    }
}
```

- `Errno` carries the number, so a caller can `match` on it as on any error.
- The second `Close` fails, since the descriptor is closed already: `EBADF`, 9.

### Choose a module per target

A package that builds for Linux and Windows names one module, `pid`, with a directory per target:

```mk
// verdict: parse
const Package = pkg.Package(
    name: "sel",
    mk: "0.1",
    bins: [pkg.Bin(name: "sel", root: "src/main", subsystem: "console")],
    targets: [
        pkg.Select(os: "linux", modules: [pkg.Module(name: "pid", dir: "src/pid/linux")]),
        pkg.Select(os: "windows", modules: [pkg.Module(name: "pid", dir: "src/pid/windows")]),
    ],
    unsafe_modules: ["src/pid/linux", "src/pid/windows"],
)
```

`src/pid/linux/pid.mk`, the only file that imports `std.linux`:

```mk
// verdict: parse
import std.linux

// Current is this process's id, from getpid(2).
export Current(): uint {
    unsafe "getpid takes no arguments and cannot fail" {
        return linux.Syscall(39, 0, 0, 0, 0, 0, 0).r
    }
}
```

`src/pid/windows/pid.mk`:

```mk
// verdict: ok
extern GetCurrentProcessId(): u32

// Current is this process's id, from GetCurrentProcessId.
export Current(): uint {
    unsafe "GetCurrentProcessId takes no arguments and cannot fail" {
        return uint(GetCurrentProcessId())
    }
}
```

`src/main/main.mk`, the same on both targets:

```mk
// verdict: parse
import pid

export Main(sys: &System): WriteError!void {
    try sys.Print("pid {p}\n", (p: pid.Current()))
}
```

```text
$ mk run
pid 30956
```

- The output is from Windows; the number differs on every run. `mk check --target x86_64-linux` and `mk check --all-targets` pass for the package as well.
- Without the selection, `import std.linux` anywhere in the package is E0202 on Windows.

## API

<!-- api:begin -->
Stability: Stable.

### `Syscall`

```
export Syscall(nr: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): (r: uint, errno: int)
```

Makes the Linux system call nr with the arguments a0 to a5, through the
x86_64 `syscall` instruction; pass 0 for arguments the call does not
use. r is the kernel's result as it came back. When that result, read
as a signed number, is from -4095 to -1, the call failed and errno is
its negation (9 for EBADF); otherwise errno is 0. Needs unsafe
(E1101): what each argument means is the call's.

It is one system call and nothing more: no retry on EINTR, and no task
suspension, so a call that blocks blocks the carrier thread under it.
Run such a call through runtime.Blocking, which takes it off the
carriers. O(1) besides the call; allocates nothing.

<!-- api:end -->
