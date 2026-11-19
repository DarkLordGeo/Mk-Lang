# std.vm

`std.vm` reserves, commits and protects address space by the page, and makes memory for generated code. It is for programs that manage memory below the level of an allocator: a JIT, a garbage collector's heap, a sandbox's cage, an arena that grows in place. Everything else should use the [allocators](README.md#3-memory-allocators-and-arenas).

Every function and method of `std.vm` is callable only in `unsafe` (E1101). A `Region` hands out raw addresses whose pages may be inaccessible, and a `CodeRegion` runs whatever bytes it is given. A module that uses it needs permission in its manifest's `unsafe_modules` ([packages](../packages.md#2-the-manifest)).

```mk
// verdict: parse
import std.vm
```

## Contents

- [The main pieces](#the-main-pieces)
- [Regions](#regions)
- [Code regions](#code-regions)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [reserve and commit](#reserve-and-commit), [generate and run code](#generate-and-run-code), [grow a code space](#grow-a-code-space)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Find the page size | `PageSize` |
| Reserve address space | `Reserve`, which returns a `Region` |
| Make pages usable, or drop them | `Region.Commit`, `Region.Decommit` |
| Make pages read-only or inaccessible | `Region.Protect` with a `Prot` |
| Give the space back | `Region.Release` |
| Memory for generated code | `CodeRegion.New` (committed) or `CodeRegion.Reserve` (committed by the page) |
| Write code, then run it | `CodeRegion.Writable`, `CodeRegion.Flush`, `CodeRegion.Entry` |
| Lock written code against writes | `CodeRegion.Seal`, `CodeRegion.Unseal` |

## Regions

A `Region` is reserved address space: a range of addresses that nothing else in the process will use, with no memory behind it yet. Reserving costs no memory, so a program can reserve gigabytes and commit only what it touches.

- `Commit(offset, len)` makes pages read-write. A newly committed page reads as zero and counts against the OS's commit limit. Pages already committed keep their contents and protection.
- `Decommit(offset, len)` drops the pages' contents and their commit charge. The addresses stay reserved, and an access faults until the next `Commit`.
- `Protect(offset, len, p)` sets committed pages to `None`, `Read` or `ReadWrite`. A page that is not committed is `Os(487)` on every target.
- `Prot` has no executable value: no page of a `Region` can run code.
- Offsets and lengths are whole pages inside the region, else `Misaligned`; for `Decommit`, which cannot fail, it is a panic with code 2.
- A `Region` is linear: end it with `Release`, which gives back the whole range.

Two tasks that commit overlapping ranges at once must exclude each other themselves.

## Code regions

A `CodeRegion` is memory for code that the program generates. It has two views of the same memory: a writable one, which you write instructions into, and an executable one, which runs them. No page is writable and executable in one view (W^X).

1. Write the bytes into `Writable()`.
2. Call `Flush(offset, len)`, the instruction-cache flush.
3. Turn `Entry(offset)`, the executable address, into a `fn` value with `raw.FnFromAddr[F](...)`, and call it.

- `Seal` makes the writable view read-only, so a stray write faults until `Unseal`. It never touches the executable view, so code at `Entry` keeps running throughout.
- `CodeRegion.New(size)` commits every page. `CodeRegion.Reserve(size)` commits none, and `Commit` takes pages as code grows; while the region is sealed, the pages `Commit` takes are read-only through the writable view. A write to a page not committed faults, and so does a call into one, as a fault in a code region.
- A fault in a code region goes to the fault filters of [std.runtime](runtime.md#fault-filters), never to Mk's report of a fault in Mk code. The runtime tracks code regions for this in a table of 64, so `New` and `Reserve` fail with `Unsupported` while 64 are live.
- `Entry` panics with code 2 for an offset outside `[0, Size())`.
- On Windows a code region is a pagefile-backed section mapped twice; on Linux a memfd mapped twice.

## Errors

`vm.Error` has four members:

| Member | Meaning |
|---|---|
| `Misaligned` | An offset, length or size is not a whole number of pages, or is outside the region |
| `OutOfMemory` | The OS has no such range, or refuses the commit charge |
| `Unsupported` | This target cannot do it, or 64 code regions are live |
| `Os(code)` | Any other refusal, with the OS's code (`GetLastError` on Windows, `errno` on Linux) |

A `Commit` that fails part way leaves the pages it committed before the failure committed.

## Memory and cost

`std.vm` never allocates from a Mk allocator. A `Region` keeps one bit per page in a bitmap of its own, a second reservation rounded up to a page. That is one page for up to 128 MiB of 4 KiB pages, 1 MiB for 32 GiB. On Linux the reservation is made with `MAP_NORESERVE`, so even a 32 GiB reservation costs no commit charge, and the bitmap is not charged either. On Windows the bitmap is charged.

`Reserve` and `Release` are a few system calls. `Commit`, `Decommit`, `Protect`, `Seal` and `Unseal` cost one system call per run of pages they change, plus a scan of the bitmap at 64 pages per word.

## Examples

These programs use `unsafe`, so they run as the root module of a package whose `pkg.mk` lists it, for example:

```mk
// verdict: parse
const Package = pkg.Package(
    name: "demo",
    bins: [pkg.Bin(name: "demo", root: "src", subsystem: "console")],
    unsafe_modules: ["src"],
)
```

with the program in `src/main.mk`, run with `mk run .`.

### Reserve and commit

```mk
// verdict: ok
import std.raw
import std.vm

export Main(sys: &System): vm.Error|WriteError!void {
    unsafe "every address used below is inside r and committed read-write" {
        page := vm.PageSize()
        // Reserve 1 GiB of address space. No page is committed yet, so this
        // costs no memory.
        var r := try vm.Reserve(1 << 30, page)
        onerr r.Release()
        try r.Commit(0, page) // the first page: read-write, all zeros
        p := raw.FromAddr[u64](r.Base())
        try sys.Print("fresh page reads {v}\n", (v: raw.Read(p)))
        raw.Write(p, 1234)
        try sys.Print("after a write: {v}\n", (v: raw.Read(p)))
        r.Decommit(0, page) // contents and charge dropped, no access
        try r.Commit(0, page)
        try sys.Print("recommitted page reads {v}\n", (v: raw.Read(p)))
        match r.Protect(page, page, vm.Prot.Read) { // the second page is not committed
            ok(_) {}
            err(e) { try sys.Print("Protect of an uncommitted page: {e}\n", (e)) }
        }
        r.Release()
    }
}
```

```text
$ mk run .
fresh page reads 0
after a write: 1234
recommitted page reads 0
Protect of an uncommitted page: Os(code: 487)
```

- `raw.FromAddr` turns an address into a pointer; reads and writes through it are the `unsafe` block's to justify.
- `onerr r.Release()` gives the space back if a later line fails, which the compiler requires of a linear `Region`.

### Generate and run code

```mk
// verdict: ok
import std.raw
import std.vm

// x86-64 machine code for a function that returns n: mov eax, n; ret.
WriteReturn(w: mut []u8, at: int, n: u8) {
    code: [6]u8 = [0xB8, n, 0, 0, 0, 0xC3]
    (mut w[at..at + 6]).CopyFrom(code[..])
}

export Main(sys: &System): vm.Error|WriteError!void {
    unsafe "the region holds whole functions at offsets 0 and 64, flushed before each call" {
        var c := try vm.CodeRegion.New(4096) // committed whole
        onerr c.Release()
        WriteReturn(c.Writable(), 0, 42) // write through the writable view
        c.Flush(0, 6)                    // make the bytes visible to execution
        f := raw.FnFromAddr[fn(): u32](c.Entry(0)) // run through the executable view
        try sys.Print("f() = {v}\n", (v: f()))

        try c.Seal() // the writable view is read-only; f still runs
        try sys.Print("sealed, f() = {v}\n", (v: f()))
        try c.Unseal()
        WriteReturn(c.Writable(), 64, 7) // a second function
        c.Flush(64, 6)
        g := raw.FnFromAddr[fn(): u32](c.Entry(64))
        try sys.Print("g() = {v}\n", (v: g()))
        c.Release()
    }
}
```

```text
$ mk run .
f() = 42
sealed, f() = 42
g() = 7
```

- The bytes are x86-64 machine code.
- A `fn` value made by `raw.FnFromAddr` is dead after `Release`.
- [Resume from a fault](runtime.md#resume-from-a-fault) uses a code region with a fault filter.

### Grow a code space

```mk
// verdict: ok
import std.raw
import std.vm

// x86-64 machine code for a function that returns n: mov eax, n; ret.
WriteReturn(w: mut []u8, at: int, n: u8) {
    code: [6]u8 = [0xB8, n, 0, 0, 0, 0xC3]
    (mut w[at..at + 6]).CopyFrom(code[..])
}

export Main(sys: &System): vm.Error|WriteError!void {
    unsafe "each function is written into a committed page and flushed before its call" {
        page := vm.PageSize()
        // Room for 1,024 pages of code, none committed: a JIT's code space.
        var c := try vm.CodeRegion.Reserve(1024 * page)
        onerr c.Release()
        try sys.Print("reserved {n} pages\n", (n: c.Size() / page))
        var next := 0 // the first page not yet used
        for n in [3, 5, 8] {
            try c.Commit(next, page) // one more page, as the code grows
            WriteReturn(c.Writable(), next, Trunc[u8](n))
            c.Flush(next, 6)
            f := raw.FnFromAddr[fn(): u32](c.Entry(next))
            try sys.Print("page {p}: f() = {v}\n", (p: next / page, v: f()))
            next += page
        }
        c.Release()
    }
}
```

```text
$ mk run .
reserved 1024 pages
page 0: f() = 3
page 1: f() = 5
page 2: f() = 8
```

- `CodeRegion.Reserve` commits nothing, so reserving 1,024 pages costs no memory. Each `Commit` adds pages that read as zero.
- A call into a page that is not committed faults, as a fault in a code region.

## API

<!-- api:begin -->
Stability: Stable.

### `CodeRegion`

```
export linear struct CodeRegion
```

Memory for generated code, seen through two views: a writable one and
an executable one, never both on one page (W^X). Code is written
through Writable, made visible by Flush, and run at Entry. On Windows
it is a pagefile-backed section mapped twice; on Linux a memfd mapped
twice.

Seal makes the writable view read-only between batches of writes, and
Unseal makes it writable again. Neither touches the executable view, so
code at Entry runs throughout. A region from New is committed whole;
one from Reserve has no page committed until Commit takes it. It is
linear: end it with Release.

### `CodeRegion.Commit`

```
export CodeRegion.Commit(&self, offset: int, len: int): Error!void
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Commits [offset, offset + len) of a region from Reserve. Its pages not
yet committed read as zero and become writable through the writable
view (read-only while the region is sealed) and executable through the
other. Pages already committed keep their contents and protection. On
a region from New, whose pages are all committed, it only checks the
range. Concurrent calls, and a Seal or Unseal at the same time, are the
caller's to exclude.

Fails with Misaligned for a range that is not whole pages inside the
region, and Os when the OS refuses; the runs committed before it stay
committed. O(len / (64 pages)) bitmap words, plus one system call per
run of uncommitted pages (two on Linux); allocates nothing.

### `CodeRegion.Entry`

```
export CodeRegion.Entry(&self, offset: int): uint
```

The executable address of the byte at offset, to pass to
raw.FnFromAddr, which makes the fn value. An offset outside
[0, Size()) panics with the bounds code (2). O(1); allocates nothing.

### `CodeRegion.Flush`

```
export CodeRegion.Flush(&self, offset: int, len: int)
```

Makes the bytes written at [offset, offset + len) visible to execution:
the instruction-cache flush, which is FlushInstructionCache on Windows
and a fence on x86_64 Linux, whose caches are coherent. Call it after
writing code and before running it. A range outside the region is
clipped to it. O(1); allocates nothing.

### `CodeRegion.New`

```
export CodeRegion.New(size: int): Error!CodeRegion
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

A code region of size bytes, rounded up to whole pages, committed
whole.

Fails with Misaligned for a size that is not positive, Os when the OS
refuses (Os(38), ENOSYS, on x86_64-nutshell), and Unsupported
while 64 code regions are live: a fault in a 65th could not reach
runtime.AddFaultFilter's filters. O(1) system calls, and a scan of the
64 region slots; allocates nothing on the heap.

### `CodeRegion.Release`

```
export CodeRegion.Release(self)
```

Unmaps both views, and gives back a reserved region's commit bitmap.
O(1) system calls; allocates nothing.

### `CodeRegion.Reserve`

```
export CodeRegion.Reserve(size: int): Error!CodeRegion
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

A code region of size bytes, rounded up to whole pages, reserved with
no page committed. A page can be written and run only once Commit takes
it. A write to a page not committed faults, and so does a call into
one, as a fault in a code region, which goes to
runtime.AddFaultFilter's filters. On Windows it is a section made
SEC_RESERVE; on Linux a memfd with both views mapped with no access.

Fails as New does, and with OutOfMemory when its commit bitmap (one bit
per page rounded up to a page, as a Region's) cannot be made. O(1)
system calls, and a scan of the 64 region slots; allocates nothing on
the heap.

### `CodeRegion.Seal`

```
export CodeRegion.Seal(&self): Error!void
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Seals the region: every committed page of the writable view becomes
read-only, so a write through Writable faults, as an access fault in Mk
code, until Unseal. Pages that Commit takes while sealed are committed
read-only. The executable view is not touched: code at Entry runs
throughout. Sealing a sealed region seals it again.

Fails with Os when the OS refuses. The region then counts as sealed
though some pages may still be writable, so the caller seals again or
releases it. O(Size / (64 pages)) bitmap words, plus one system call
per run of committed pages (one in all for a region from New);
allocates nothing.

### `CodeRegion.Size`

```
export CodeRegion.Size(&self): int
```

The region's size in bytes, a whole number of pages. O(1); allocates
nothing.

### `CodeRegion.Unseal`

```
export CodeRegion.Unseal(&self): Error!void
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Unseals the region: every committed page of the writable view becomes
writable again, for the next batch of writes. The executable view is
not touched.

Fails with Os when the OS refuses. The region then stays sealed, and
the pages made writable before the refusal are sealed again as far as
the OS allows. O(Size / (64 pages)) bitmap words, plus one system call
per run of committed pages (one in all for a region from New);
allocates nothing.

### `CodeRegion.Writable`

```
export CodeRegion.Writable(&self): mut []u8 from self
```

- Flows: The result borrows from self.

The writable view's bytes, to write code into. What they hold is seen
through the executable view after Flush. A write through them faults
while the region is sealed, and on a page that a region from Reserve
has not committed. O(1); allocates nothing.

### `Error`

```
export type Error = OutOfMemory | Misaligned | Unsupported | Os
```

Every way a std.vm operation fails.

### `Misaligned`

```
export error Misaligned
```

An offset, length or size is not a whole number of pages, or is
outside the region.

### `Os`

```
export error Os(code: i64)
```

The OS refused, with its code (GetLastError on Windows, errno on Linux).

### `PageSize`

```
export PageSize(): int
```

The OS's page size in bytes. Every offset and length a Region takes is
a multiple of it. O(1); allocates nothing.

### `Prot`

```
export enum Prot
```

A page protection for Region.Protect. There is no executable value: no
page is both writable and executable (W^X), and code runs only from a
CodeRegion's executable view.

Variants:

- `None`: No access.
- `Read`: Read only.
- `ReadWrite`: Read and write.

### `Region`

```
export linear struct Region
```

Reserved address space: size bytes from Base, with no access until
Commit. It is linear: end it with Release, which gives back the whole
range.

### `Region.Base`

```
export Region.Base(&self): uint
```

The first address of the region, which a program reads and writes
through once it has committed pages. O(1); allocates nothing.

### `Region.Commit`

```
export Region.Commit(&self, offset: int, len: int): Error!void
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Commits [offset, offset + len). Its uncommitted pages become read-write
and read as zero, charged against the OS's commit limit. Pages already
committed keep their contents and protection, on every target.
Concurrent calls on overlapping ranges are the caller's to exclude.

Fails with OutOfMemory when the OS refuses the charge, and the runs of
pages committed before it stay committed; with Misaligned for a range
that is not whole pages inside the region. O(len / (64 pages)) bitmap
words, plus one system call per run of uncommitted pages; allocates
nothing.

### `Region.Decommit`

```
export Region.Decommit(&self, offset: int, len: int)
```

Drops the contents and the commit charge of [offset, offset + len). The
address space stays reserved, with no access. A range that is not whole
pages inside the region panics with the bounds code (2). One system
call, plus one bitmap read-modify-write per 64 pages; allocates
nothing. O(len / (64 pages)).

### `Region.Protect`

```
export Region.Protect(&self, offset: int, len: int, p: Prot): Error!void
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Sets the protection of the committed pages [offset, offset + len) to
None (any access faults), Read or ReadWrite; never executable.

Fails with Os(487) when a page in the range is not committed, on every
target: 487 is Windows' ERROR_INVALID_ADDRESS, and std gives the same
code on Linux. Fails with Misaligned for a range that is not whole
pages inside the region. One bitmap load per 64 pages and one system
call; allocates nothing. O(len / (64 pages)).

### `Region.Release`

```
export Region.Release(self)
```

Gives back the whole reservation, committed pages included, and the
commit bitmap's. O(1): two system calls; allocates nothing.

### `Region.Size`

```
export Region.Size(&self): int
```

The region's size in bytes, a whole number of pages. O(1); allocates
nothing.

### `Reserve`

```
export Reserve(size: int, align: int): Error!Region
```

- Errors: Error (OutOfMemory, Misaligned, Os(code: i64), Unsupported)

Reserves size bytes of address space, a whole number of pages, at an
address that is a multiple of align. align is a power of two; an align
below a page acts as a page. No page is committed: on Linux the
reservation is MAP_NORESERVE, so a large reservation (32 GiB) costs no
commit charge.

The Region tracks committed pages in a bitmap, a second reservation of
one bit per page rounded up to a page (one page for up to 128 MiB of
4 KiB pages, 1 MiB for 32 GiB), committed at once: charged on Windows,
not on Linux.

Fails with Misaligned for a size or align of the wrong form, and
OutOfMemory when the OS has no such range. O(1) system calls (three);
allocates nothing on the heap.

### `Unsupported`

```
export error Unsupported
```

This target cannot do it, or 64 code regions are live.

<!-- api:end -->
