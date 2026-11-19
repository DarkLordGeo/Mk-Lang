// libc from tasks in system-libc mode: 64 tasks call
// libc on whatever carrier runs them: close(-1), then that thread's errno
// (EBADF, 9) before any await, then malloc and free. It prints how many
// calls saw the right errno, whether a task ran off the main thread (a
// carrier pthread_create made), getpid against the raw system call's
// answer, strlen, and qsort sorting through an Mk callback.
import std.atomic
import std.raw

extern close(fd: i32): i32
extern __errno_location(): *i32
extern malloc(n: uint): *u8
extern free(p: *u8)
extern gettid(): i32
extern getpid(): i32
extern strlen(s: *u8): uint
extern qsort(base: *u8, n: uint, size: uint, cmp: fn(*u8, *u8): i32)

const Calls: int = 200
const Tasks: int = 64

// Work makes Calls rounds of libc calls on its carrier and counts the
// rounds whose errno was EBADF; off counts a task that ran on a thread
// other than the main one.
Work(main_tid: i32, ok: &atomic.AtomicInt, off: &atomic.AtomicInt) {
    var spin := 0
    for i in 0..Calls {
        unsafe "libc: close of an invalid fd, the thread's errno word, and a block malloc gives back" {
            r := close(-1)
            e := *__errno_location()
            p := malloc(Trunc[uint](64 + i))
            free(p)
            if r == -1 && e == 9 {
                _ = ok.Add(1)
            }
            if gettid() != main_tid {
                _ = off.Add(1)
            }
        }
        for k in 0..2000 {
            spin = spin +% k
        }
    }
    if spin == 1 {
        _ = ok.Add(0)
    }
}

// Cmp orders two i64 elements for qsort.
Cmp(a: *u8, b: *u8): i32 {
    unsafe "qsort passes two elements of the i64 array it sorts" {
        x := raw.Read(raw.PtrCast[u8, i64](a))
        y := raw.Read(raw.PtrCast[u8, i64](b))
        return if x < y { -1 } else if x > y { 1 } else { 0 }
    }
}

export Main(sys: &System): WriteError!int {
    ok := atomic.AtomicInt.New(0)
    off := atomic.AtomicInt.New(0)
    var main_tid: i32 = 0
    unsafe "gettid reads nothing" {
        main_tid = gettid()
    }
    scope s := sys.io.Scope() {
        for _ in 0..Tasks {
            s.go Work(main_tid, &ok, &off)
        }
    }
    try sys.Print("errno ok {n} of {t}\n", (n: ok.Load(), t: Tasks * Calls))
    try sys.Print("off main thread {b}\n", (b: off.Load() > 0))
    var pid: i32 = 0
    var len: uint = 0
    var xs: [6]i64 = [5, -3, 9, 0, 2, -8]
    unsafe "getpid reads nothing; strlen reads a NUL-terminated constant; qsort sorts xs in place through Cmp" {
        pid = getpid()
        len = strlen(raw.StrData("system-libc\u{0}"))
        qsort(raw.PtrCast[i64, u8](raw.DataMut(mut xs[..])), 6, 8, Cmp)
    }
    try sys.Print("getpid equals the main tid {e}\n", (e: pid == main_tid))
    try sys.Print("strlen {len}\n", (len))
    try sys.Print("qsort {a} {b} {c} {d} {e} {f}\n", (a: xs[0], b: xs[1], c: xs[2], d: xs[3], e: xs[4], f: xs[5]))
    return 0
}
