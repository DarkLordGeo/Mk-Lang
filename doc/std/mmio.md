# std.mmio

`std.mmio` reads and writes memory-mapped device registers. Each access happens exactly as written: one load or store of exactly the register's width, never merged with another, split, left out or moved past another register access. Reach for it in a kernel, a boot loader or a driver that talks to hardware through memory addresses. For ordinary memory shared between tasks, use [`std.atomic`](atomic.md) instead; for x86 port I/O (`in`, `out`), use [`asm`](../reference.md#inline-assembly).

`std.mmio` is provisional: it may change in a minor release. Read [unsafe code](../reference.md#14-unsafe-and-c) in the reference first.

```mk
// verdict: parse
import std.mmio
```

## Contents

- [The main pieces](#the-main-pieces)
- [Rules](#rules)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [read a register at a fixed address](#read-a-register-at-a-fixed-address), [wait for a device](#wait-for-a-device), [load and store through a pointer](#load-and-store-through-a-pointer), [what the compiler refuses](#what-the-compiler-refuses)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| A register at a fixed address | `Reg[T].New(addr)`, then `Reg.Read` and `Reg.Write` |
| Read or write through a raw pointer | `Load(p)`, `Store(p, x)` |
| Order every memory access around a point | `Fence()` |

`Reg[T]` holds the address and nothing else. `Reg.Read` is `Load` of that address, and `Reg.Write` is `Store`.

## Rules

- Every call is inside an `unsafe` block (E1101 elsewhere). The block's reason says why the address is a device register mapped for this program, usually uncached. The module that holds the block is listed in the manifest's `unsafe_modules` (E1102, [packages](../packages.md#2-the-manifest)).
- `T` is an integer type, `int`, `uint` or a raw pointer: an access is one instruction of exactly `T`'s width. A float, a struct or an array is E0701.
- An access is never merged, split, left out or reordered against another `std.mmio` access. Ordinary loads and stores may still move around it; put a `Fence()` where they must not, for example between filling a buffer in memory and telling the device to read it.
- `Fence()` orders every memory access before it against every one after it, as a sequentially consistent fence.
- No call can run at compile time: a `const` initializer that reaches one is E0901 (`refused: volatile`).
- A freestanding program (`x86_64-freestanding`) may import `std.mmio`: it needs no `Io` and no runtime.

## Memory and cost

Each call compiles inline to one instruction (a volatile load, a volatile store, or a fence) and allocates nothing. `Reg[T]` is the size of a pointer.

## Examples

A real device register exists only on the machine and in the mode that maps it, so the first two programs compile but are not run here. The third runs anywhere, with ordinary memory in place of a register.

### Read a register at a fixed address

```mk
// verdict: ok
import std.mmio

// HpetCounter reads the main counter of the HPET timer.
HpetCounter(): u64 {
    unsafe "the HPET is mapped uncached at 0xFED00000" {
        counter := mmio.Reg[u64].New(0xFED000F0) // the main counter, offset 0xF0
        return counter.Read()                     // one 64-bit load
    }
}
```

- `New` makes no access. Each `Read` is one load, even when two follow each other with nothing between them.

### Wait for a device

```mk
// verdict: ok
import std.mmio

// A 16550-style serial port mapped at 0x1000_0000: data at offset 0,
// line status at offset 5, where bit 0x20 means "ready for a byte".
const UartBase: uint = 0x1000_0000

PutByte(b: u8) {
    unsafe "the serial port is mapped uncached at UartBase" {
        status := mmio.Reg[u8].New(UartBase + 5)
        data := mmio.Reg[u8].New(UartBase)
        for status.Read() & 0x20 == 0 {} // every turn reads the register again
        data.Write(b)
    }
}

PutStr(s: str) {
    for b in s.AsBytes() {
        PutByte(b)
    }
}
```

- The compiler never hoists `status.Read()` out of the loop, as it might an ordinary load: the device changes the register.
- Registers are `u8` here, so each access is one byte wide, as the device expects.

### Load and store through a pointer

The program runs as a package whose `pkg.mk` lists the module in `unsafe_modules`, which its `unsafe` block and its `export extern var` both need:

```text
scratch/
    pkg.mk     const Package = pkg.Package(name: "scratch", bins: [pkg.Bin(name: "scratch", root: ".")], unsafe_modules: ["."])
    main.mk    the program below
```

```mk
// verdict: ok
import std.mmio

export extern var Scratch: u32 // ordinary memory standing in for a register

export Main(sys: &System): WriteError!void {
    unsafe "Scratch is ordinary memory standing in for a device register" {
        p := &Scratch // the address of an extern var is a raw *u32
        mmio.Store(p, 0x2A)
        mmio.Fence()
        v := mmio.Load(p)
        try sys.Print("{v}\n", (v))
    }
}
```

```text
$ mk run .
42
```

- `Load` and `Store` take a raw pointer `*T`, so they work with an address from a linker symbol, from `std.raw` or from a C function. `Reg` takes the address as a `uint`.

### What the compiler refuses

```mk
// verdict: reject E1101
import std.mmio

Ready(p: *u8): bool {
    return mmio.Load(p) & 1 != 0 // <- E1101
}
```

```mk
// verdict: reject E0701
import std.mmio

// A volatile access is one instruction of an integer or raw pointer type.
ReadF(p: *f64): f64 {
    unsafe "a device register" {
        return mmio.Load(p) // <- E0701
    }
}
```

- Read a float register as the integer of its width (`u64` for `f64`) and convert it with `f64.FromBits`.
- A `const` that calls `HpetCounter` above is E0901: the compiler cannot read a device.

## API

<!-- api:begin -->
Stability: Provisional.

### `Fence`

```
export Fence()
```

Fence orders every memory access before it against every one after it, as a
sequentially consistent fence. It needs unsafe (E1101).

### `Load`

```
export Load[T: Plain](p: *T): T
```

- Flows: The result borrows from p.

Load reads *p as one volatile access of exactly T's width, never merged,
split, elided or reordered against another volatile access. It needs unsafe
(E1101); T is an integer type, int, uint or a raw pointer (E0701). Not
available at compile time (E0901).

### `Reg`

```
export struct Reg[T: Plain]
```

Reg is a device register of type T at a fixed address. Each of its methods
needs unsafe (E1101), as Load and Store do.

### `Reg.New`

```
export Reg[T].New(addr: uint): Reg[T]
```

New is the register at address addr.

### `Reg.Read`

```
export Reg[T].Read(&self): T
```

- Flows: The result borrows what self refers to; self is free after the call.

Read is one volatile load of the register, as Load.

### `Reg.Write`

```
export Reg[T].Write(&self, x: T)
```

Write is one volatile store of x to the register, as Store.

### `Store`

```
export Store[T: Plain](p: *T, x: T)
```

Store writes x to *p as one volatile access of exactly T's width, never
merged, split, elided or reordered against another volatile access. It needs
unsafe (E1101); T is an integer type, int, uint or a raw pointer (E0701).
Not available at compile time (E0901).

<!-- api:end -->
