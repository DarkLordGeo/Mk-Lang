# std.mem

`std.mem` adds allocators and a pool to the prelude's `Arena` and `Heap`.

- `Fixed` serves memory from a buffer you give it.
- `Counting` counts what passes through another allocator.
- `Null` refuses everything.
- `Custom` is an allocator over your own functions.
- `Pool` stores values under generational handles.
- `OffsetOf` and `Prefetch` help with layout.

Reach for it to put a hard budget on memory, or to check in a test that code frees what it allocates or allocates nothing. Its pool lets values name each other without references.

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.mem
```

## Contents

- [The main pieces](#the-main-pieces)
- [Allocators](#allocators)
- [A budget for an arena](#a-budget-for-an-arena)
- [Pools and handles](#pools-and-handles)
- [Layout](#layout)
- [Examples](#examples): [a list in a fixed buffer](#a-list-in-a-fixed-buffer), [an arena on a budget](#an-arena-on-a-budget), [count allocations](#count-allocations), [prove a path allocates nothing](#prove-a-path-allocates-nothing), [nodes that name each other](#nodes-that-name-each-other)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Allocate from a buffer you own, a stack array included | `Fixed.New(buf)`, then `Fixed.Alloc()`; `Fixed.Reset` |
| Count bytes and allocations, find leaks | `Counting.New(inner)`, then `Counting.Alloc()`; `Bytes`, `Live`, `Peak` |
| Check that code allocates nothing | `Null()` |
| An allocator over your own functions | `Custom(ctx, table)` with an `AllocTable` (unsafe) |
| Store values under handles, reuse freed slots | `Pool[T]`: `Add`, `Get`, `GetMut`, `GetPair`, `Free`, `Iter`, `Sweep` |
| A field's byte offset | `OffsetOf[T]("field")` |
| A prefetch hint | `Prefetch(p, write)` |

Each allocator gives an `&Alloc` (`Custom` an `Alloc`, which you pass as `&`), which a heap container (`HeapList`, `HeapMap`, `Box`, `coll.HeapDeque`, ...) or `Arena.NewIn` takes.

## Allocators

- **`Fixed`** is a bump allocator over a caller's buffer, and never touches the system. An allocation is O(1). Only the last block can be freed, and freeing it gives back the alignment padding before it too. So frees in any last-in, first-out order return the buffer to where it was. The last block also grows in place, so a lone growing list costs no copies. Each block starts on at least an 8-byte boundary, so a block under 8 bytes takes 8, and up to 7 bytes at the start of the buffer go unused. `Reset` frees everything at once; it ends the `Fixed`, so nothing allocated from it can be used after it.
- **`Counting`** passes every request to another allocator and counts the bytes and allocations live through it, and the most bytes it has seen live. It fails exactly when the inner allocator does. `Live()` of zero at the end of a test means nothing leaked.
- **`Null()`** fails every request with `OutOfMemory`. Code that runs to the end with it allocated nothing on that run.
- **`Custom(ctx, table)`** makes an `Alloc` over a program's own functions, for an embedder that hands Mk its own heap. Each request is one call of the table's `alloc`, `resize` or `free` entry with `ctx`. `Custom`, and the `Alloc.Bytes` and `FreeBytes` calls that reach the table, are callable only inside `unsafe` (E1101), since the entries are trusted to keep the table's rules. `AllocTable` and `Custom` are Provisional.

`Fixed.Alloc()` and `Counting.Alloc()` return an `&Alloc` that borrows the `Fixed` or the `Counting`, so a container made from it keeps that value in place while it lives. `Counting.New(inner)` borrows `inner`, and `Fixed.New(buf)` borrows `buf` for writing.

No `&Alloc` crosses to another task (E1002), and `Fixed` and `Counting` are not `Share`, so each `Fixed` or `Counting` serves one task at a time. `Null()` holds no state: any task may call it.

## A budget for an arena

`Arena.NewIn(a)` makes an arena whose chunks, the first included, and its `Sub`s' chunks come from the allocator `a`, and go back to it at `Reset` and `Free`. So a `Counting` or `Fixed` under it bounds everything an API that takes `&Arena` puts in the arena. The example [an arena on a budget](#an-arena-on-a-budget) shows one.

- The arena borrows `a`, and each `Sub` borrows the arena. So the allocator behind `a` is not reset, freed or moved while either lives.
- An `Arena` is not `Send`, so it never calls `a` from another task.
- A `Fixed` gets back only what is freed in last-in, first-out order. An arena that served a value aligned over 8 bytes, or a `Sub` freed before its parent's later chunks, may leave part of the buffer used until `Fixed.Reset`.
- A std API that also uses the process heap says so in its documentation, as [`proc.Run`](proc.md) and a [`sim.Sim`](sim.md) do.

## Pools and handles

A `Pool[T]` keeps its values in slots in its arena, each slot with a generation, and reuses a freed slot. `Add` returns a `Handle`: 8 bytes, plain, `Eq` and `Hash`, with no public fields, naming the slot and its 32-bit generation. Values can store handles to each other where references would tie them to one borrow. Wrap a `Handle` in a one-field struct to keep two kinds of handle apart.

- A handle whose value was freed gets `none` from `Get` and `GetMut` and `false` from `Free`: never a panic, never another value. A slot whose generation is spent, after 2^32 - 1 reuses, is retired and never reused, so a stale handle never matches again.
- A handle from another pool is refused the same way unless its slot and generation match a live value there. A handle has no room for its pool's identity, so each pool starts its generations at its own base. Between two pools a false match takes about 2^31 reuses of one slot; among the N pools a process has made, the nearest bases are about 2^32 / N apart. A false match is never memory-unsafe, since every value of a pool is a `T`.
- `Free` drops the value, as `Sweep` does, so each needs a `T` that is not linear (E0701 at the call). Inside a generic function, the bounds must say `T` is not linear, as `T: Plain` does.
- `Iter` visits the live values in slot order. `Sweep` frees every value whose `keep` returns false and returns how many it freed: the sweep of a mark-and-sweep collector.
- `GetPair` gives two values for writing, and returns `none` when the two handles are equal or stale, never a panic.

`Add`, `Get`, `GetMut`, `GetPair`, `Free` and `Len` are O(1); a whole `Iter` pass is O(n), and `Sweep` is n calls of `keep`, n the slots ever used. `Add` allocates only when the pool grows, doubling its buffer in the arena, which keeps the old one until the arena is freed. A failed `Add` changes nothing and leaves the value with the caller. `Free` and `Sweep` may allocate in the arena to record a freed slot; when that fails the slot is retired, so they never fail.

```mk
// verdict: reject E0701
// Sweep drops what it does not keep, so its values are not linear
import std.fs
import std.mem

Purge(p: mut mem.Pool[fs.File]): int {
    return p.Sweep(fn(h: mem.Handle, f: &fs.File): bool { return false }) // <- E0701
}

PurgeAll[T](p: mut mem.Pool[T]): int {
    return p.Sweep(fn(h: mem.Handle, x: &T): bool { return false }) // <- E0701
}
```

## Layout

`OffsetOf[T]("field")` is the byte offset of a field in `T`'s layout, as a compile-time value. A field `T` lacks is E0901. `Prefetch(p, write)` tells the CPU that `p`'s memory will soon be read, or written; it changes nothing a program can observe. Both are safe, O(1) and allocate nothing.

```mk
// verdict: ok
import std.mem

struct Header {
    tag: u8
    size: u32
    next: u64
}

const NextAt: int = mem.OffsetOf[Header]("next") // a compile-time value

export Main(sys: &System): WriteError!void {
    try sys.Print("size at {s}, next at {n}, {total} bytes\n", (s: mem.OffsetOf[Header]("size"), n: NextAt, total: SizeOf[Header]()))
}
```

```text
$ mk run layout.mk
size at 4, next at 8, 16 bytes
```

## Examples

Each program below runs as written with `mk run`.

### A list in a fixed buffer

```mk
// verdict: ok
import std.mem

export Main(sys: &System): OutOfMemory|WriteError!void {
    var buf: [300]u8 = [0; 300]
    f := mem.Fixed.New(mut buf[..]) // every byte comes from buf
    var xs := HeapList[int].New(f.Alloc())
    onerr xs.Free(f.Alloc())
    for i in 0..100 {
        xs.Push(f.Alloc(), i) else {
            try sys.Print("full after {n} values\n", (n: xs.Len()))
            break
        }
    }
    xs.Free(f.Alloc())
}
```

```text
$ mk run fixed.mk
full after 32 values
```

- The list doubles its capacity as it grows, in place since it is the last block. 32 values take 256 bytes; 64 would take 512, more than the buffer holds.
- A failed `Push` leaves the list as it was, so it is still freed at the end.

### An arena on a budget

```mk
// verdict: ok
import std.mem

// Distinct counts the different words of text; its map lives in a.
Distinct(a: &Arena, text: str into a): OutOfMemory!int {
    var seen := Map[str, bool].New(a)
    for w in text.Words() {
        try seen.Set(w, true)
    }
    return seen.Len()
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var buf: [4096]u8 = [0; 4096]
    f := mem.Fixed.New(mut buf[..])
    var scratch := try Arena.NewIn(f.Alloc()) // every chunk of the arena comes from buf
    onerr scratch.Free()
    n := try Distinct(&scratch, "the cat saw the dog and the bird")
    try sys.Print("{n} distinct words\n", (n))
    scratch.Free()
}
```

```text
$ mk run budget.mk
6 distinct words
```

- `Distinct` takes an ordinary `&Arena` and knows nothing of the budget. Past 4,096 bytes it fails with `OutOfMemory` instead of reaching the heap.

### Count allocations

```mk
// verdict: ok
import std.mem

export Main(sys: &System): OutOfMemory|WriteError!void {
    c := mem.Counting.New(sys.heap.Alloc()) // counts what passes through it
    a := c.Alloc()
    x := try Box[i64].New(a, 1)
    onerr x.Free(a)
    y := try Box[i64].New(a, 2)
    onerr y.Free(a)
    try sys.Print("live {l}, bytes {b}\n", (l: c.Live(), b: c.Bytes()))
    x.Free(a)
    y.Free(a)
    try sys.Print("live {l}, bytes {b}, peak {p}\n", (l: c.Live(), b: c.Bytes(), p: c.Peak()))
}
```

```text
$ mk run count.mk
live 2, bytes 16
live 0, bytes 0, peak 16
```

- `Bytes` counts the sizes callers asked for, not what the inner allocator rounded them up to.
- A negative `Live` means something freed through the `Counting` a block it did not allocate.

### Prove a path allocates nothing

```mk
// verdict: ok
import std.mem

// Sum adds xs up and allocates nothing.
Sum(a: &Alloc, xs: []int): OutOfMemory!int {
    var total := 0
    for x in xs {
        total += x
    }
    return total
}

// Evens collects the even values of xs in a list, so it allocates.
Evens(a: &Alloc, xs: []int): OutOfMemory!int {
    var even := HeapList[int].New(a)
    onerr even.Free(a)
    for x in xs {
        if x % 2 == 0 {
            try even.Push(a, x)
        }
    }
    n := even.Len()
    even.Free(a)
    return n
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    xs := [3, 1, 2]
    n := try Sum(mem.Null(), xs[..]) // it succeeded, so Sum allocated nothing
    try sys.Print("sum {n}\n", (n))
    _ = Evens(mem.Null(), xs[..]) else {
        try sys.Print("Evens allocates\n")
    }
}
```

```text
$ mk run null.mk
sum 6
Evens allocates
```

- `Null` proves something about one run: a path that allocates only on some inputs passes on the others.

### Nodes that name each other

```mk
// verdict: ok
import std.mem

struct Node {
    name: str
    next: ?mem.Handle // a handle, not a reference, so nodes can name each other
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var p := mem.Pool[Node].New(sys.arena)
    b := try p.Add(Node(name: "b", next: none))
    a := try p.Add(Node(name: "a", next: some(b)))
    if n := p.Get(a) {
        if nb := n.next {
            try sys.Print("a -> {name}\n", (name: p.Get(nb).Must().name))
        }
    }
    _ = p.Free(b)
    try sys.Print("b after Free: {gone}, Free again: {again}\n", (gone: p.Get(b).IsNone(), again: p.Free(b)))
    c := try p.Add(Node(name: "c", next: none)) // reuses b's slot
    try sys.Print("old b names c: {same}, live: {n}\n", (same: p.Get(b).IsSome(), n: p.Len()))
    dropped := p.Sweep(fn(h: mem.Handle, n: &Node): bool { return n.name != "c" })
    try sys.Print("swept {d}, live: {n}\n", (d: dropped, n: p.Len()))
    _ = c
}
```

```text
$ mk run pool.mk
a -> b
b after Free: true, Free again: false
old b names c: false, live: 2
swept 1, live: 1
```

- `Add` puts a value in the slot freed last, so `c` takes `b`'s slot with a new generation, and the old handle `b` still gets `none`.
- `Sweep` keeps the values for which `keep` returns true and frees the rest.

## API

<!-- api:begin -->
Stability: Stable.

### `AllocTable`

```
export struct AllocTable
```

A table of a program's own allocator functions, which Custom calls:
alloc(ctx, n, align) returns a block or null, resize(ctx, p, old, new,
align) resizes in place or returns false, and free(ctx, p, n, align)
frees. Build one with `mem.AllocTable(alloc: ..., resize: ..., free:
...)`. Provisional, as Custom is.

Fields:

- `alloc: fn(*u8, int, int): *u8`: alloc(ctx, size, align) returns a block, or null on failure.
- `resize: fn(*u8, *u8, int, int, int): bool`: resize(ctx, p, old, new, align) grows or shrinks the block in place, or
  returns false.
- `free: fn(*u8, *u8, int, int)`: free(ctx, p, size, align) gives the block back.

### `Counting`

```
export view struct Counting
```

An allocator that passes every request to another and counts the bytes
and allocations live through it, and the peak.

### `Counting.Alloc`

```
export Counting.Alloc(&self): &Alloc from self
```

- Flows: The result borrows from self.

The allocator that counts and passes each request on to inner; it
fails exactly when inner does. O(1) per request on top of inner's.

### `Counting.Bytes`

```
export Counting.Bytes(&self): int
```

The bytes allocated through self and not freed: the sizes callers
asked for, not what inner rounded them up to. O(1).

### `Counting.Live`

```
export Counting.Live(&self): int
```

The allocations made through self and not freed. Zero at the end of a
test means nothing leaked; a negative count means something freed
through self a block that self did not allocate. O(1).

### `Counting.New`

```
export Counting.New(inner: &Alloc): Counting
```

- Flows: The result borrows from inner.

A Counting over inner, with every count zero. The result borrows inner.
O(1); allocates nothing.

### `Counting.Peak`

```
export Counting.Peak(&self): int
```

The most Bytes() has been since New. O(1).

### `Custom`

```
export Custom(ctx: *u8, table: &AllocTable): Alloc from table
```

- Flows: The result borrows from table.

An Alloc whose every request is one call of an entry of table, with
ctx. table must outlive the Alloc and every block it hands out.
Callable only inside unsafe (E1101), since the entries are trusted to
keep the table's rules. O(1); allocates nothing.

### `Fixed`

```
export view struct Fixed
```

A bump allocator over a caller's buffer, which never touches the
system: an allocation is O(1), the last block grows and frees in place,
and Reset frees everything at once.

Only the last block can be freed, and freeing it gives back the
alignment padding before it too, so frees in any last-in, first-out
order return the buffer to where it was. Each block starts on at least
an 8-byte boundary, so a block under 8 bytes takes 8. Alignments up to
2^30 are served; a larger one fails.

### `Fixed.Alloc`

```
export Fixed.Alloc(&self): &Alloc from self
```

- Flows: The result borrows from self.

The allocator that hands out self's buffer; a request it cannot fit
fails with OutOfMemory. A container made from it borrows self. O(1).

### `Fixed.New`

```
export Fixed.New(buf: mut []u8): Fixed
```

- Flows: The result borrows from buf.

A Fixed over buf, which stays borrowed (mut) for as long as the Fixed
and anything allocated from it lives. Up to 7 bytes in front of buf's
first 8-byte boundary are never handed out; a buf with no such boundary
(fewer than 8 bytes, or an empty one at an odd address) refuses every
request. O(1); allocates nothing.

### `Fixed.Reset`

```
export Fixed.Reset(mut self) ends self
```

- Flows: Ends every borrow of self.

Makes the whole buffer free again. It ends self, so nothing allocated
from self can be used after it. O(1).

### `Handle`

```
export struct Handle
```

The name of a value in a Pool: 8 bytes, plain, Eq and Hash, with no
public fields. It holds a slot index and that slot's 32-bit generation.
To keep two kinds of handle apart, wrap each in a one-field struct.

### `Null`

```
export Null(): &Alloc
```

An allocator on which every request fails with OutOfMemory. Code that
runs to the end with it allocated nothing on that run. O(1).

### `OffsetOf`

```
export OffsetOf[T](field: str): int
```

The byte offset of the field named field in T's layout, as a
compile-time value; a field T lacks is E0901. It allocates nothing.

### `Pool`

```
export view struct Pool[T]
```

Values of T in slots of an arena, each named by a Handle that goes
stale when its value is freed. A freed slot is reused with a new
generation, so a stale handle gets none, never another value.

A handle carries no pool identity, so each pool starts its generations
at its own base. Another pool's handle is refused unless its slot and
generation match a live value here: between two pools that takes about
2^31 reuses of one slot, and among the N pools a process made the
nearest bases are about 2^32 / N apart. A false match is never
memory-unsafe, since every value of a pool is a T.

### `Pool.Add`

```
export Pool[T].Add(mut self, x: T into self): OutOfMemory!Handle
```

- Errors: OutOfMemory

Stores x and returns its handle: in the slot freed last when there is
one, else in a new slot at the end. Fails with OutOfMemory when the
arena cannot grow the pool or the pool holds 2^32 - 1 slots; then
nothing changed and the caller keeps x. Amortized O(1); a growth
doubles the buffer in the arena, which keeps the old one.

### `Pool.Free`

```
export Pool[T].Free(mut self, h: Handle): bool
```

Drops h's value, makes every handle to it stale and returns true;
false, with nothing changed, for a handle that names no live value of
this pool. T may not be linear (E0701 at the call; in a generic
function, bound T by Plain). The slot is reused by a later Add, unless
its generation is spent after 2^32 - 1 reuses: then it is retired, so
a stale handle never matches again. When the arena cannot record the
free slot, the slot is retired too, so Free never fails. O(1).

### `Pool.Get`

```
export Pool[T].Get(&self, h: Handle): ?&T
```

- Flows: The result borrows from self.

The value h names; none for a stale, foreign or out-of-range handle,
never a panic. O(1).

### `Pool.GetMut`

```
export Pool[T].GetMut(mut self, h: Handle): ?mut T
```

- Flows: The result borrows from self.

The value h names, to update in place; none as Get. O(1).

### `Pool.GetPair`

```
export Pool[T].GetPair(mut self, h: Handle, k: Handle): ?(a: mut T, b: mut T) from self
```

- Flows: The result borrows from self.

The values of h and k, both writable; none when h and k name the same
slot or either names no live value of this pool, never a panic. O(1).

### `Pool.Iter`

```
export Pool[T].Iter(&self): PoolIter[T] from self
```

- Flows: The result borrows from self.

Iterates the live values in slot order. O(1); a whole pass is O(n), n
the slots ever used. It allocates nothing.

### `Pool.Len`

```
export Pool[T].Len(&self): int
```

The number of live values. O(1).

### `Pool.New`

```
export Pool[T].New(a: &Arena): Pool[T]
```

- Flows: The result borrows from a.

An empty pool in a. O(1); allocates nothing until the first Add.

### `Pool.Sweep`

```
export Pool[T].Sweep(mut self, const keep: fn(Handle, &T): bool): int
```

Drops every live value whose keep returns false, as Free does, so its
handles go stale, and returns how many it dropped: the sweep of a
mark-and-sweep collector. keep may capture locals. T may not be linear
(E0701 at the call). n calls of keep, n the slots ever used.

### `PoolIter`

```
export view struct PoolIter[T]
```

The live values of a pool with their handles, in slot order; made by
Pool.Iter.

### `PoolIter.Next`

```
export PoolIter[T].Next(mut self): ?(handle: Handle, value: &T) from self.pool
```

- Flows: The result borrows what self.pool refers to; self is free after the call.

The next live value and its handle, or none after the last.

### `Prefetch`

```
export Prefetch[T](p: &T, write: bool)
```

A hint that p's memory will soon be read, or written when write is
true; it changes nothing a program can observe. O(1); allocates
nothing.

<!-- api:end -->
