# std.derive

`std.derive` writes the three everyday interfaces for a plain struct or enum: `Eq` to compare two values, `Hash` to use them as map keys, and `Format` to print them. Each is a generator: a `gen derive.Eq(T)` line writes `impl Eq for T` while the program compiles. The impl covers `T`'s fields in declaration order, so no field is forgotten when the type grows. Write the `impl` by hand instead when equality is not field by field, for example when a field is a cache that should not count.

[Generators](../reference.md#generators) in the reference explain how a `gen` line works.

```mk
// verdict: parse
import std.derive
```

## Contents

- [The main pieces](#the-main-pieces)
- [What each generator writes](#what-each-generator-writes)
- [Enums](#enums)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [compare and print](#compare-and-print), [a struct as a map key](#a-struct-as-a-map-key), [a set of enum values](#a-set-of-enum-values)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Compare two values field by field | `gen derive.Eq(T)`, then `a.Eq(&b)` |
| Use a struct or enum as a `Map` key | `gen derive.Eq(T)` and `gen derive.Hash(T)` |
| Print a value with `{}` | `gen derive.Format(T)` |

The `gen` line goes at the top level of the module that declares `T`. Each field's type must implement the interface. A field whose struct has no `Eq` needs its own `gen derive.Eq` line too; the order of the `gen` lines does not matter.

## What each generator writes

| Generator | Writes | Behaviour |
|---|---|---|
| `derive.Eq(T)` | `impl Eq for T` | Compares each field with its type's own `Eq`, in declaration order, and returns `false` at the first difference |
| `derive.Hash(T)` | `impl Hash for T` | Feeds each field to the `Hasher` through its type's own `Hash`, in declaration order. Two values that are `Eq` field by field hash alike |
| `derive.Format(T)` | `impl Format for T` | Writes `Name(field: value, ...)`, each value through its own `Format`; a struct with no fields prints `Name()` |

- `Eq` is a method: call it as `a.Eq(&b)`. The operator `==` stays undefined on `T` (E0308).
- `Format` is the structural form of printing, the one to use for a debug print: format specs have no `?` or `#` form. Each field prints through its own `Format` ([formatting](core.md#formatting-and-printing)), so a `str` field prints as written, without quotes.

## Enums

For an enum, the generators work variant by variant, in declaration order:

- `Eq` compares the variants first; for the same variant, it compares the payload fields.
- `Hash` feeds the variant's index first, so `.A` and `.B` with equal payloads hash differently.
- `Format` writes the variant's name, then `(field: value, ...)` when the variant has a payload: `Dot`, `Circle(center: Pos(x: 1, y: 2), r: 5)`.

## Errors

| Code | When |
|---|---|
| E0903 | `T` is not a struct or an enum, or is an enum with no variants. Reported at `T`'s declaration, or at the `gen` line when `T` is a built-in type |
| E0905 | The output does not compile, most often because a field's type does not implement the interface. Reported at the `gen` line, with the inner error |
| E0704 | `T` already has that interface, from a second `gen` line or a handwritten `impl`, or `T` is declared in another module. Reported at the `gen` line |

```mk
// verdict: reject E0903
import std.derive

interface Named { // <- E0903
    Name(&self): str
}

gen derive.Eq(Named) // only a struct or an enum can be derived
```

The floats have no `Hash`, so a struct with a float field cannot derive it:

```mk
// verdict: reject E0905
import std.derive

struct Reading {
    sensor: int
    value: f64
}

gen derive.Hash(Reading) // <- E0905
```

## Memory and cost

The generators run at compile time: O(fields) each, with the emitted code in the compiler's own arena. The code they write allocates nothing: `Eq` and `Hash` are a call per field, and `Format` writes to its `Writer` and fails only when the `Writer` does. The generated code uses layer 0 only ([layers](README.md#1-layers)), so a freestanding program may use it.

## Examples

### Compare and print

```mk
// verdict: ok
import std.derive

struct Pos {
    x: int
    y: int
}

enum Shape {
    Dot
    Circle(center: Pos, r: int)
    Label(text: str)
}

gen derive.Eq(Pos)
gen derive.Format(Pos)
gen derive.Eq(Shape) // needs Pos's Eq for the Circle payload
gen derive.Format(Shape)

export Main(sys: &System): WriteError!void {
    p := Pos(x: 1, y: 2)
    q := Pos(x: 1, y: 2)
    try sys.Print("{p}\n", (p))
    try sys.Print("p == q: {e}\n", (e: p.Eq(&q)))
    shapes := [Shape.Dot, Shape.Circle(center: p, r: 5), Shape.Label(text: "home")]
    for s in shapes {
        try sys.Print("{s}\n", (s))
    }
    try sys.Print("same variant, other payload: {e}\n", (e: shapes[1].Eq(&Shape.Circle(center: q, r: 6))))
}
```

```text
$ mk run shapes.mk
Pos(x: 1, y: 2)
p == q: true
Dot
Circle(center: Pos(x: 1, y: 2), r: 5)
Label(text: home)
same variant, other payload: false
```

### A struct as a map key

```mk
// verdict: ok
import std.derive

struct Cell {
    row: int
    col: int
}

gen derive.Eq(Cell)
gen derive.Hash(Cell) // with Eq, a Cell can be a Map key
gen derive.Format(Cell)

export Main(sys: &System): OutOfMemory|WriteError!void {
    var seen := Map[Cell, int].New(sys.arena)
    moves := [Cell(row: 0, col: 0), Cell(row: 1, col: 2), Cell(row: 0, col: 0)]
    for c in moves {
        n := try seen.Ensure(c, 0) // inserts 0 the first time
        *n += 1
    }
    try sys.Print("{n} cells\n", (n: seen.Len()))
    for c in [Cell(row: 0, col: 0), Cell(row: 1, col: 2), Cell(row: 5, col: 5)] {
        if n := seen.Get(&c) {
            try sys.Print("{c}: {n}\n", (c, n))
        } else {
            try sys.Print("{c}: never\n", (c))
        }
    }
}
```

```text
$ mk run cells.mk
2 cells
Cell(row: 0, col: 0): 2
Cell(row: 1, col: 2): 1
Cell(row: 5, col: 5): never
```

### A set of enum values

```mk
// verdict: ok
import std.derive

enum Key {
    Char(c: char)
    Enter
    F(n: int)
}

gen derive.Eq(Key)
gen derive.Hash(Key)
gen derive.Format(Key)

export Main(sys: &System): OutOfMemory|WriteError!void {
    var pressed := Set[Key].New(sys.arena)
    for k in [Key.Char(c: 'q'), Key.Enter, Key.F(n: 1), Key.Char(c: 'q'), Key.Enter] {
        added := try pressed.Add(k) // false when an equal key is already there
        try sys.Print("{k}: {s}\n", (k, s: if added { "new" } else { "again" }))
    }
    try sys.Print("F(n: 2) pressed: {b}\n", (b: pressed.Has(&Key.F(n: 2))))
}
```

```text
$ mk run keys.mk
Char(c: q): new
Enter: new
F(n: 1): new
Char(c: q): again
Enter: again
F(n: 2) pressed: false
```

- `F(n: 1)` and `F(n: 2)` are the same variant with different payloads, so they are different keys.

## API

<!-- api:begin -->
Stability: Stable.

### `Eq`

```
export Eq(g: mut Gen, t: Type)
```

The generator behind `gen derive.Eq(T)`: it writes `impl Eq for T`,
whose Eq compares every field with the field type's own Eq, in
declaration order, and is true when all are equal. For an enum it
compares the variants first, then the payload fields of the matching
variant. Call it as `a.Eq(&b)`: `==` stays undefined on T.

E0903 at T when T is not a struct or an enum, or is an enum with no
variants. A field whose type has no Eq is E0905 at the gen line, and a
second Eq for T is E0704. Runs at compile time, O(fields); allocates
the emitted code in the run's arena. The emitted Eq allocates nothing.

### `Format`

```
export Format(g: mut Gen, t: Type)
```

The generator behind `gen derive.Format(T)`: it writes
`impl Format for T`, so `{}` prints a T. A struct prints as
`Name(f1: v1, f2: v2)`, or `Name()` with no fields; an enum value prints
as its variant's name, followed by `(f1: v1, ...)` when the variant has a
payload. Each value prints through its own Format, so a str field
prints as written, without quotes. This is the structural form of
printing: there is no debug format spec.

E0903 and E0905 as for Eq. Runs at compile time, O(fields); allocates
the emitted code in the run's arena. The emitted Format writes to its
Writer and fails only when the Writer does.

### `Hash`

```
export Hash(g: mut Gen, t: Type)
```

The generator behind `gen derive.Hash(T)`: it writes `impl Hash for T`,
whose Hash feeds every field to the Hasher through the field type's own
Hash, in declaration order, so two values that are Eq field by field
hash alike. For an enum it feeds the variant's index first, so .A and .B
with equal payloads hash differently. With derive.Eq, T can be a Map
key.

E0903 and E0905 as for Eq. Runs at compile time, O(fields); allocates
the emitted code in the run's arena. The emitted Hash allocates nothing.

<!-- api:end -->
