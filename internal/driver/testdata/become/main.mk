// become: chains of 10,000,000 tail
// calls, direct, through a table of fn values, of a generic function and of
// a method. Each frame reads its stack pointer and the chain keeps the
// lowest value seen. The value at the start, once the first 1,000 calls
// have run (the first may be inlined into Main, in a frame of its own
// size), and the value at the end must be equal: with become each call
// replaces the frame, so the stack never grows. Without it the chain would
// need about 10,000,000 frames and overflow the stack.

struct Vm {
    k: int
}

// Big is 24 bytes: passed through memory and returned through a hidden
// pointer on every target, so Carry forwards its own parameter's memory
// and its caller's result pointer.
struct Big {
    a: int
    b: int
    c: int
}

// Low is lo lowered to sp; Top is the lowest value at the start: lo once
// the first 1,000 calls of the chain have run, top before and after.
Low(lo: u64, sp: u64): u64 {
    if sp < lo {
        return sp
    }
    return lo
}

Top(top: u64, lo: u64, n: int): u64 {
    if n == 9999000 {
        return lo
    }
    return top
}

// Done is the chain's answer: its count when the lowest stack pointer at
// the end is the one at the start, else -1.
Done(acc: int, lo: u64, top: u64): int {
    if lo == top {
        return acc
    }
    return -1
}

Direct(n: int, acc: int, lo: u64, top: u64): int {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Done(acc, Low(lo, sp), Top(top, Low(lo, sp), n))
    }
    become Direct(n - 1, acc + 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

Even(n: int, acc: int, lo: u64, top: u64): int {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Done(acc, Low(lo, sp), Top(top, Low(lo, sp), n))
    }
    become Ops[n % 2](n - 1, acc + 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

Odd(n: int, acc: int, lo: u64, top: u64): int {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Done(acc, Low(lo, sp), Top(top, Low(lo, sp), n))
    }
    become Ops[n % 2](n - 1, acc + 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

const Ops: [2]fn(int, int, u64, u64): int = [Even, Odd]

Gen[T: Plain](x: T, n: int, lo: u64, top: u64): int {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Done(10000000, Low(lo, sp), Top(top, Low(lo, sp), n))
    }
    become Gen[T](x, n - 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

Vm.Run(&self, n: int, lo: u64, top: u64): int {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Done(self.k, Low(lo, sp), Top(top, Low(lo, sp), n))
    }
    become self.Run(n - 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

Carry(b: Big, n: int, lo: u64, top: u64): Big {
    var sp: u64 = 0
    unsafe "reads the stack pointer" {
        asm("mov {sp}, rsp", out(reg) sp, options(nomem, nostack, preserves_flags))
    }
    if n == 0 {
        return Big(a: b.a + b.b + b.c, b: Done(10000000, Low(lo, sp), Top(top, Low(lo, sp), n)), c: 0)
    }
    become Carry(b, n - 1, Low(lo, sp), Top(top, Low(lo, sp), n))
}

// Many has nine parameters: past the registers of Win64 (4) and System V
// (6), so musttail reuses the caller's incoming stack argument area.
Many(a: int, b: int, c: int, d: int, e: int, f: int, g: int, h: f64, n: int): int {
    if n == 0 {
        return a + b + c + d + e + f + g + Cast[int](h)
    }
    become Many(b, c, d, e, f, g, a, h + 1.0, n - 1)
}

export Main(sys: &System): WriteError!void {
    n := 10000000
    max: u64 = 18446744073709551615
    d := Direct(n, 0, max, 0)
    f := Even(n, 0, max, 0)
    g := Gen[i64](7, n, max, 0)
    vm := Vm(k: n)
    m := vm.Run(n, max, 0)
    r := Carry(Big(a: 1, b: 2, c: 3), n, max, 0)
    try sys.Print("direct {d}\nfn table {f}\ngeneric {g}\nmethod {m}\n", (d: d, f: f, g: g, m: m))
    try sys.Print("memory {a} {b}\n", (a: r.a, b: r.b))
    s := Many(1, 2, 3, 4, 5, 6, 7, 0.0, n)
    try sys.Print("stack {s}\n", (s: s))
}
