# kernel

A small x86_64 kernel written in Mk for `x86_64-freestanding`, booted by QEMU's PVH loader. It brings up a serial console, reads the boot memory map, builds its own page tables, hands the usable memory to a page allocator, routes interrupts through an IDT of naked stubs, the local APIC and the 8259 PIC, runs the APIC timer, gives `free.Heap()` its pages, and then serves a command interpreter on the serial line. It shows Mk with no operating system: `naked` functions, `asm`, `std.mmio`, `std.free`, link scripts and a panic hook, with no assembly file.

## Running it

From the program's folder, build both images and boot one in QEMU:

```
cd Dogfoods/kernel
mk build --target x86_64-freestanding -o out
qemu-system-x86_64 -kernel out/kernel -serial stdio -device isa-debug-exit,iobase=0xf4,iosize=0x04 -m 128M -display none -monitor none -no-reboot
```

Typing `mem`, `pages` and `exit` at the prompt gives:

```text
mk kernel: booting
boot: 7 memory regions, 28640 pages managed from 0x1000000
boot: local APIC 0 version 0x14 takes the interrupts
boot: timer: 3 interrupts taken
boot: breakpoint: handled, 1 so far
boot: frame allocator: took 8 pages at 0x1000000 and gave them back
type help for the commands
> mem
memory map: 7 regions, 133692416 bytes usable
  0000000000000000 - 000000000009fc00  usable
  000000000009fc00 - 00000000000a0000  reserved
  00000000000f0000 - 0000000000100000  reserved
  0000000000100000 - 0000000007fe0000  usable
  0000000007fe0000 - 0000000008000000  reserved
  00000000fffc0000 - 0000000100000000  reserved
  000000fd00000000 - 0000010000000000  reserved
frame allocator: 28640 pages managed, 0 in use
> pages
pages: 28640 managed, 0 in use, 28640 free
heap hooks: 0 runs taken, 0 given back
> exit
bye: 0 commands failed, status 16
```

and QEMU exits with status 33. QEMU's exit status is `(code << 1) | 1` for the code the kernel writes to port `0xf4`:

| Status | Code | Meaning |
|---|---|---|
| 33 | `0x10` | `exit`, or 5 s with no input, and no command failed |
| 35 | `0x11` | `exit` after a failed command; `crashtest`'s read did not fault |
| 37 | `0x12` | a panic, reported by the panic hook |
| 39 | `0x13` | a boot step failed before the shell started |
| `2N+1` | `N` | `exit N`, `N` from 0 to 127 |

| Command | What it does |
|---|---|
| `help` | List the commands |
| `mem` | The boot memory map |
| `pages` | Allocator and heap-hook counters |
| `alloc N`, `free ADDR \| all` | Take N contiguous physical pages (1 to 4096), give them back |
| `heap N` | Sum 0 to N-1 in a `HeapList` on `free.Heap()`; a large `N` runs the heap out of memory |
| `ticks N` | Wait for N local APIC timer interrupts, 1 to 100 |
| `apic` | The local APIC's id and version |
| `break` | A breakpoint through the IDT |
| `echo`, `stat [reset]` | Echo the words; the serial counters |
| `crash KIND` | Panic on purpose: `must`, `bounds`, `overflow`, `divide`, `opcode`, `page` |
| `bench N` | N rounds of page, heap and spin-lock churn, with a checksum |
| `exit [CODE]` | End the session |

Every error prints `error: ...` and makes the final status 35. A second image, `crashtest`, boots the same modules and faults on purpose, so the whole crash path runs: fault stub, handler, `Panic`, `MkPanicHook`.

## Design

| Module | What |
|---|---|
| `src/boot` | The PVH note (`section ".note.Xen"`), the 32-bit `naked` entry that builds the boot identity map and enters long mode, the 64-bit entry, and the boot stack, tables and GDT as `align`ed Mk `var`s |
| `src/arch` | Port I/O in `asm`, the 16550 UART, the IDT with `naked` stubs for the timer, COM1, the breakpoint and four faults, the local APIC through `mmio.Reg`, `mmio.Load`, `mmio.Store` and `mmio.Fence`, the PIC, the kernel's page tables, the serial input ring |
| `src/mm` | The PVH start info's memory map, the bitmap page allocator behind an `atomic.Spin`, and `MkPageAlloc` and `MkPageFree`, the page source of `free.Heap()` |
| `src/console` | COM1's transmitter behind an `atomic.Spin`; a `Console` that `Print` formats onto the line through a `text.Sink` over the UART, allocating nothing; line input from the ring; `MkPanicHook`, which prints `free.PanicInfo` through a UART of its own and takes no lock |
| `src/shell` | The command interpreter; each line's words are listed in a `mem.Fixed` area |
| `src` | The kernel's main line and boot self-test |
| `crash` | The `crashtest` image's main line |

- `link.ld` and `crash.ld` place the 32-bit entry first at 1 MiB; the boot module calls the `KernelMain` that each image's root module exports.
- Serial input arrives by interrupt into a ring behind a spin lock that the kernel takes only with interrupts disabled, so on one CPU the handler and the kernel never wait for each other.
- The page allocator is first-fit from the lowest address, so a fresh boot hands out the same addresses every time and the cases' outputs are exact.

## Limits

- One CPU; no user mode, no scheduler, no virtual memory beyond the identity map of the first GiB and the APIC page; the page pool starts at 16 MiB and ends at 1 GiB.
- Input comes by interrupt into a 512-byte ring. QEMU on Windows drops serial input the guest's UART cannot take at once (its stdio backend has no flow control), so typing faster than the kernel drains loses bytes there.
- Needs QEMU (`qemu-system-x86_64`) on `PATH` or under `%ProgramFiles%\qemu` to run.

## Tests

Each case boots QEMU: `.in` is typed into the serial line, `.out` is the serial output and `.code` QEMU's exit status. They cover the boot path, every command, bad numbers and lines, one panic of each kind, and `crashtest`'s page fault. Run them from the repository root with `go run ./tools/dogfood run -only kernel`; without QEMU the harness only checks the ELF.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
Print[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void
Writer.Str(mut self, s: str): WriteError!void
Writer.Bytes(mut self, b: []u8): WriteError!void
Writer.Flush(mut self): WriteError!void
Heap.Alloc(&self): &Alloc
// std.atomic
AtomicInt.New(n: i64): AtomicInt
AtomicInt.Add(&self, n: i64): i64
AtomicInt.Load(&self): i64
AtomicBool.New(b: bool): AtomicBool
AtomicBool.Load(&self): bool
AtomicBool.Store(&self, b: bool)
Atomic[T].New(x: T): Atomic[T]
Atomic[T].Load(&self, const o: Order): T
Atomic[T].Store(&self, x: T, const o: Order)
Spin[T].New(x: T): Spin[T]
Spin[T].Lock(&self): SpinGuard[T]
Spin[T].Into(self): T
SpinGuard[T].Ref(&self): &T
SpinGuard[T].Mut(mut self): mut T
SpinGuard[T].Store(mut self, x: T into self)
SpinGuard[T].Unlock(self)
const Relaxed: Order = Order(v: 0)
const Acquire: Order = Order(v: 1)
const Release: Order = Order(v: 2)
atomic.Fence(const o: Order)
// std.mmio. Reg below is mmio.Reg
*
// std.free, on x86_64-freestanding only
*
// std.mem
Fixed.New(buf: mut []u8): Fixed
Fixed.Alloc(&self): &Alloc from self
Fixed.Reset(mut self) ends self
// std.text
Sink[S].New(write: fn(mut S, []u8): WriteError!void): Sink[S]
Sink[S].Writer(&self, s: mut S, buf: mut []u8): Writer from self, s, buf
```
