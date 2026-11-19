# std.free

`std.free` is the small runtime of a freestanding program: one that runs with no operating system, such as a kernel, a boot loader or firmware, built for `x86_64-freestanding`. It hands a panic's report to the program's hook and gives the program its one `Heap`. Only a freestanding program can import it (E0202 elsewhere). A hosted program's panics and heap are in [std.runtime](runtime.md#panics) and `sys.heap`.

Read [freestanding](../reference.md#freestanding) in the reference first.

```mk
// verdict: parse
import std.free
```

## Contents

- [The main pieces](#the-main-pieces)
- [Panics](#panics)
- [The heap and its pages](#the-heap-and-its-pages)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [give the heap its pages](#give-the-heap-its-pages), [report a panic on a serial port](#report-a-panic-on-a-serial-port), [run without a heap](#run-without-a-heap)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Decide what a panic does | export `MkPanicHook(info: &free.PanicInfo): never` |
| Read a panic's report | `PanicInfo`: `code`, `message`, `file`, `line`, `column` |
| Get a heap | `free.Heap()`, then `.Alloc()` for the `&Alloc` that heap containers take |
| Give the heap memory | export `MkPageAlloc(size: int, align: int): *u8` and `MkPageFree(p: *u8, size: int, align: int)` |

`std.free` exists on `x86_64-freestanding` only. [Freestanding](../reference.md#freestanding) in the reference says what such a program may import and how it starts. The hooks above are functions the program exports with `export extern`.

## Panics

A panic in a freestanding program, a failed bounds check or `Panic("...")` alike, calls the program's `MkPanicHook` with a `PanicInfo`. Without one, it executes a trap instruction. The hook does not return.

- `code` is the panic code, numbered as on hosted targets ([panic codes](runtime.md#panics)).
- `message` is the text given to `Panic`, or a contract's text, and otherwise the code's name, such as `"overflow"`, `"bounds"` or `"must"`. `Must` and `Assert` give the code's name, since a freestanding program has no table of error names.
- `file`, `line` and `column` are where the check failed.

Making the report allocates nothing and prints nothing: what to do with it is the program's choice. Nothing unwinds. [`text.Capped`](text.md) and `text.Sink` format text with no allocator, for a hook that writes the message out.

## The heap and its pages

`free.Heap()` returns the one `Heap` of the program, in static memory, since there is no `System` to hand one out. The heap does no bookkeeping of its own: the program's two hooks are the allocator.

- Each allocation is one call of `MkPageAlloc(size, align)`, with the size and alignment asked for. It returns that many bytes so aligned, or null when it has none, which the caller sees as `OutOfMemory`.
- Each free is one call of `MkPageFree(p, size, align)`, with a block `MkPageAlloc` returned and the same size and alignment.
- A block never grows in place: a growing container allocates a new block and copies.

The freestanding runtime defines both hooks as weak defaults that return null and do nothing. So a program that uses the heap without exporting them gets `OutOfMemory` from every allocation.

The first call of `free.Heap()` sets the heap up. Make it once at boot, before the kernel starts another CPU; a call that races it spins until the first is done.

## Memory and cost

`free.Heap()` is O(1) after its first call and allocates nothing itself. An allocation or a free through the heap costs one call of your hook, and what the hook does. `std.free` holds a few words of static memory.

## Examples

The programs are packages for `x86_64-freestanding`, with `unsafe_modules: ["src"]` in their `pkg.mk`. They build with `mk build --target x86_64-freestanding`, which writes a static ELF executable. They need a machine or an emulator to run, and a link script and boot code for a bootable image (`link_script` and `asm_sources`, [freestanding](../reference.md#freestanding)), so no output is shown.

### Give the heap its pages

```mk
// verdict: parse
import std.free
import std.raw

const PoolSize: int = 65536

// The kernel's page pool: static memory, handed out in order and never
// given back. Next, the bytes handed out so far, is an atomic, so a global
// var of it can change.
export extern var Pool: [65536]u8
var Next: raw.Atomic[int] = raw.Atomic[int].New(0)

export extern MkPageAlloc(size: int, align: int): *u8 {
    var base: uint = 0
    unsafe "only the address of Pool is taken" {
        base = raw.Addr(&Pool)
    }
    a := Cast[uint](align)
    for {
        old := Next.Load()
        at := (base + Cast[uint](old) + a - 1) / a * a // the next address aligned to align
        end := Cast[int](at - base) + size
        if end > PoolSize {
            return raw.Null[u8]() // null: the allocation fails with OutOfMemory
        }
        if Next.Cas(old, end) {
            unsafe "at to at + size is inside Pool, and no other call hands it out" {
                return raw.FromAddr[u8](at)
            }
        }
    }
}

export extern MkPageFree(p: *u8, size: int, align: int) {} // pages are never reused

export extern MkPanicHook(info: &free.PanicInfo): never {
    for {} // a real kernel writes info.message to its console and halts
}

export extern MkEntry(): never {
    a := free.Heap().Alloc()
    var xs := HeapList[int].New(a)
    for i in 0..100 {
        xs.Push(a, i * i) else {
            Panic("out of pages")
        }
    }
    Assert(xs[99] == 9801)
    xs.Free(a)
    for {}
}
```

- `Pool` is an `export extern var`: a zeroed static symbol whose address, `&Pool` inside `unsafe`, is a raw pointer the hook can hand out. A plain `var` gives a reference, not an address.
- `MkPageAlloc` aligns the address, not the offset: nothing promises that `Pool` starts at the alignment the heap asks for.
- `Next` is a `raw.Atomic`, so `MkPageAlloc` is safe to call from two CPUs at once.
- `HeapList` and every other heap container take `free.Heap().Alloc()` as their allocator, as they take `sys.heap.Alloc()` in a hosted program.

### Report a panic on a serial port

```mk
// verdict: parse
import std.free

const Com1: u16 = 0x3F8

Out8(port: u16, v: u8) {
    unsafe "out writes one byte to an I/O port and touches no memory" {
        asm("out dx, al", in("dx") port, in("al") v, options(nomem, nostack, preserves_flags))
    }
}

Put(s: str) {
    for b in s.AsBytes() {
        Out8(Com1, b) // a real driver waits for the transmitter first
    }
}

PutInt(n: int) {
    if n >= 10 {
        PutInt(n / 10)
    }
    Out8(Com1, Trunc[u8](n % 10) + 0x30)
}

// A panic anywhere in the kernel ends here: write the report to the
// serial port, then halt.
export extern MkPanicHook(info: &free.PanicInfo): never {
    Put("panic: ")
    Put(info.message)
    Put(" at ")
    Put(info.file)
    Put(":")
    PutInt(info.line)
    Put("\n")
    for {
        unsafe "hlt stops the CPU until the next interrupt" {
            asm("hlt", options(nomem, nostack))
        }
    }
}

export extern MkEntry(): never {
    xs: [3]u8 = [1, 2, 3]
    var i := 0
    for {
        i += 1
        Out8(Com1, xs[i]) // i reaches 3: a bounds panic, code 2
    }
}
```

- When `i` reaches 3 the bounds check fails, and the hook receives code 2 with the message `"bounds"`.
- This kernel never touches the heap, so it needs no page hooks.

### Run without a heap

```mk
// verdict: parse
import std.free

// This kernel exports no MkPageAlloc, so the heap has no memory: the
// defaults return null, and every allocation is OutOfMemory.

export extern MkPanicHook(info: &free.PanicInfo): never {
    for {}
}

// Halt stops the CPU for good.
Halt(): never {
    for {
        unsafe "hlt stops the CPU until the next interrupt" {
            asm("hlt", options(nomem, nostack))
        }
    }
}

export extern MkEntry(): never {
    a := free.Heap().Alloc()
    var xs := HeapList[u8].New(a)
    xs.Push(a, 1) else {
        xs.Free(a)
        Halt() // OutOfMemory: boot without a heap, or stop here
    }
    xs.Free(a)
    Panic("the heap had memory")
}
```

- Without `MkPageAlloc` and `MkPageFree` the program still links: the weak defaults stand in, and every allocation fails with `OutOfMemory`.
- `xs` is linear, so the failure path frees it too, though it holds nothing.

## API

<!-- api:begin -->
Stability: Provisional.

### `Heap`

```
export Heap(): &Heap
```

The one Heap of a freestanding program, in static memory, since there
is no System to hand one out. Each allocation is one call of the
program's `export extern MkPageAlloc(size: int, align: int): *u8`, with
the size and alignment asked for, and each free one call of its
`MkPageFree(p: *u8, size: int, align: int)`; a block never grows in
place. Without those hooks the defaults return null, and every
allocation fails with OutOfMemory. The first call sets the Heap up; a
kernel makes it once at boot, before it starts another CPU, and a call
that races it spins until the first is done. O(1) after the first
call; allocates nothing.

### `PanicInfo`

```
export struct PanicInfo
```

A panic's report, which the program's MkPanicHook receives. code is
the panic code, numbered as on hosted targets (1 overflow, 2 bounds,
3 divide by zero, ..., 8 Panic). message is the text given to Panic
or a contract's text, and otherwise the code's name, such as
"overflow", "bounds" or "must": Must and Assert give the code's name,
since a freestanding program has no table of error names. file, line
and column are where the check failed. A program receives it by
exporting `export extern MkPanicHook(info: &free.PanicInfo): never`;
without one a panic executes a trap instruction. Making the report
allocates nothing.

Fields:

- `code: u32`
- `message: str`
- `file: str`
- `line: int`
- `column: int`

<!-- api:end -->
