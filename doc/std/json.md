# std.json

`std.json` reads and writes JSON (RFC 8259). `Parse` turns text into a `Value`, a tree you walk with `match` and a few lookup methods. `gen json.Codec(T)` writes the code that reads a `Value` into your own struct or enum and writes one back as JSON. Reach for `Value` when the shape of the input is open or only partly known, and for the codec when it has a fixed shape.

Read the [library guide](README.md) first. The cookbook has a recipe that reads a file: [Parse JSON into a struct](../cookbook.md#parse-json-into-a-struct).

```mk
// verdict: parse
import std.json
```

## Contents

- [The main pieces](#the-main-pieces)
- [Parsing](#parsing)
- [Values](#values)
- [Typed JSON with the codec](#typed-json-with-the-codec)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [walk a value](#walk-a-value), [find what is wrong](#find-what-is-wrong), [read and write a struct](#read-and-write-a-struct)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Parse text | `Parse`, which returns a `Value` |
| Look up a member | `Value.Get` |
| Read a leaf | `Value.Str`, `Value.Int`, `Value.Float`, or a `match` on the variant |
| Write a `Value` as JSON | `{v}` in a `Print`, or `Value.Format` |
| Read and write your own type | `gen json.Codec(T)`, then `T.FromJson` and `T.FormatJson` |
| Encode any codec type from generic code | the `Json` interface |

## Parsing

`Parse(a, text)` reads one JSON text into a `Value` in the arena `a`. The grammar is exact: no comments, no trailing commas, no leading zeros, no `+` sign, and no bare control characters in a string.

- Numbers keep their text, so `Parse` never rounds. Read one with `Int` or `Float` when you need it.
- A string without an escape is a view of `text`, not a copy. The signature says so with `text: str into a`: the text must live as long as the arena.
- A string with an escape is decoded into `a`. An escape that decodes to a lone surrogate (`"\ud800"`) is `Syntax`. `text` is a `str`, so it is UTF-8 already and the error set has no `Utf8Error`.
- Nesting deeper than 512 arrays and objects fails with `Depth`.
- Duplicate member names are kept in the `Value`; `Get` and the codec take the last one.

Each level of nesting is one recursive call. A dev build's frames are larger, so on a task's default stack a dev build reaches the stack limit, a panic, before 512 levels. It parses 400 levels and panics on 512. Parse untrusted text in a release build, or bound its depth first.

## Values

A `Value` is one of six variants:

| Variant | Holds |
|---|---|
| `Null` | nothing |
| `Bool(b)` | a `bool` |
| `Number(text)` | the number's text, as written |
| `Text(s)` | the decoded string |
| `Array(items)` | a `List[Value]` |
| `Object(members)` | a `List[Member]`, each with a `key` and a `value`, in input order |

- `Get(key)` is the value of the last member named `key`, or `none` when there is none or the value is not an object.
- `Str` is a `Text`'s string. `Int` is a `Number` whose text is an integer that fits `i64`, so `"1.0"` and `"1e2"` give `none`. `Float` is a `Number` as the nearest `f64`, `none` past `f64`'s range. Each gives `none` for any other variant.
- `Value` has no `Bool` method, since the variant owns the name: read one with a `match` on `.Bool(b)`.
- A `Value` formats as compact JSON, so `{v}` in a `Print` writes it. It fails with `WriteError` on a `Number` whose text is not a JSON number, which only a `Value` you built by hand can hold.

## Typed JSON with the codec

`gen json.Codec(T)`, for a struct or an enum `T`, writes two things:

- `impl json.Json for T`, whose `FormatJson(&self, w: mut Writer): WriteError!void` writes `T` as compact JSON;
- `T.FromJson(a: &Arena, v: &Value): json.Error!T from a`, which reads a `T` from a parsed `Value`.

A struct is an object of its fields, in declaration order, keyed by field name. A struct with no field writes `{}` and reads any object. An enum is tagged by variant name: a variant with no payload is the string `"V"`, and one with a payload `{"V": {fields}}`.

| Field type | JSON |
|---|---|
| `str` | a string |
| `bool` | `true` or `false` |
| the integers | a number. Signed integers and `u8` to `u32` read through `i64`; `u64` and `uint` read from decimal digits alone, so every integer the codec writes reads back. A value out of the field's range is `Mismatch` |
| `f32`, `f64` | a number. A NaN or an infinity is written as `null`, which does not read back as a float |
| `?T` | the value, or `null` for `none` |
| `[]T`, `List[T]` | an array |
| another struct or enum | its own `FormatJson` and `FromJson`, so it needs its own `gen json.Codec` line |

When reading:

- Members `T` has no field for are ignored.
- A missing or `null` member of a `?T` field reads as `none`. Any other missing member is `Mismatch`.
- `FromJson` copies every string it reads into `a`, so its result borrows `a` alone and the `Value` may end first.

Every field must be exported, since `FromJson` builds `T` from the JSON alone and a field has no default. An unexported field, or a field whose type has no JSON form (`char`, `[N]T`, a reference, `json.Value`), is error E0903 at that field. A field of a struct or enum type with no `gen json.Codec` line of its own is E0905 in the written code.

## Errors

`json.Error` is every way `Parse` and `FromJson` fail:

| Member | Meaning |
|---|---|
| `Syntax(offset)` | The text is not JSON. `offset` is the first byte that cannot continue it, or the text's length when it ends too soon |
| `Depth(offset)` | More than 512 levels of nesting. `offset` is the opening bracket of the 513th |
| `Mismatch(offset)` | `FromJson` found a value of the wrong shape, or a missing member. `offset` is -1, since a `Value` keeps no offsets |
| `OutOfMemory` | The arena is full |

`json.Error` is open: it may gain members ([errors](README.md#5-errors)).

## Memory and cost

`Parse` is O(n) in the text. It allocates the lists of arrays and objects and the decoded copy of each escaped string in `a`, and nothing for a string without an escape. `Get` scans an object's members from the end, O(members); `Int` and `Float` read the number's text. `FormatJson` and `Value.Format` allocate nothing. The codec runs when the program builds; the written `FromJson` allocates in its arena.

`Parse` and `FromJson` take no `Io` and are never cancelled. `FormatJson` and `Value.Format` write through their `Writer`: a cancel or a write failure is `WriteError`, and the writer may then hold part of the JSON.

## Examples

Each program below runs as written with `mk run`.

### Walk a value

```mk
// verdict: ok
import std.json

export Main(sys: &System): json.Error|WriteError!void {
    text := "{\"name\": \"mk\", \"stars\": 42, \"tags\": [\"lang\", \"compiler\"], \"public\": true}"
    v := try json.Parse(sys.arena, text) // strings without escapes are views of text

    // Get finds a member; Str, Int and Float read a leaf, none for another variant.
    name := v.Get("name") else { return }
    stars := v.Get("stars") else { return }
    try sys.Print("{name} has {stars} stars\n", (name: name.Str() else { "?" }, stars: stars.Int() else { 0 }))

    // An Array holds a List of Values; Value has no Bool method, so match on .Bool.
    tags := v.Get("tags") else { return }
    match tags {
        .Array(items) {
            for t in &items.Items() {
                try sys.Print("tag {t}\n", (t: t.Str() else { "?" }))
            }
        }
        _ {}
    }
    public := v.Get("public") else { return }
    match public {
        .Bool(b) { try sys.Print("public: {b}\n", (b: *b)) }
        _ { try sys.Print("public is not a bool\n", ()) }
    }

    // A Value prints as compact JSON.
    try sys.Print("{v}\n", (v: &v))
}
```

```text
$ mk run walk.mk
mk has 42 stars
tag lang
tag compiler
public: true
{"name":"mk","stars":42,"tags":["lang","compiler"],"public":true}
```

- `Get` returns a reference into the tree, `?&Value`, so nothing is copied.
- `for t in &items.Items()` walks the list by reference: a `Value` is not plain and cannot be copied out.

### Find what is wrong

```mk
// verdict: ok
import std.json

// Check parses text and prints the result or the error. The Value may hold
// views of text, so text must live as long as sys's arena: into sys.
Check(sys: &System, text: str into sys): WriteError!void {
    v := json.Parse(sys.arena, text) else e {
        try sys.Print("{text}  {e}\n", (text, e))
        return
    }
    try sys.Print("{text}  ok: {v}\n", (text, v: &v))
}

export Main(sys: &System): WriteError!void {
    try Check(sys, "[1, 2,]")             // no trailing commas
    try Check(sys, "{\"a\": 01}")         // no leading zeros
    try Check(sys, "\"\\ud800\"")         // a lone surrogate escape
    try Check(sys, "[1.50, 1e2, \"\\u00e9\"]") // numbers keep their text
    try Check(sys, "{\"k\": 1, \"k\": 2}") // duplicates are kept; Get takes the last
}
```

```text
$ mk run check.mk
[1, 2,]  Syntax(offset: 6)
{"a": 01}  Syntax(offset: 7)
"\ud800"  Syntax(offset: 1)
[1.50, 1e2, "\u00e9"]  ok: [1.50,1e2,"é"]
{"k": 1, "k": 2}  ok: {"k":1,"k":2}
```

- The offset is a byte offset into the text: the `]` after the trailing comma, the `1` after the leading zero, the backslash of the escape.
- `1.50` prints as `1.50`: the number's text is kept, not its value.

### Read and write a struct

```mk
// verdict: ok
import std.json

enum Shape {
    Point
    Circle(r: f64)
}

struct Item {
    export name: str
    export count: u32
    export shape: Shape
    export note: ?str
    export tags: List[str]
}

gen json.Codec(Shape)
gen json.Codec(Item)

export Main(sys: &System): json.Error|WriteError!void {
    var tags := List[str].New(sys.arena)
    try tags.Push("red")
    item := Item(name: "dot", count: 3, shape: Shape.Circle(r: 1.5), note: none, tags: tags)

    var out := sys.Stdout()
    try item.FormatJson(mut out) // fields in declaration order, compact
    try sys.Print("\n", ())

    // Unknown members are ignored; a missing ?T member is none.
    v := try json.Parse(sys.arena, "{\"name\":\"box\",\"count\":7,\"shape\":\"Point\",\"tags\":[],\"extra\":1}")
    back := try Item.FromJson(sys.arena, &v)
    try sys.Print("{name} x{count}, note is none: {none}\n", (name: back.name, count: back.count, none: back.note.IsNone()))

    // A missing member that is not ?T is Mismatch, with offset -1.
    w := try json.Parse(sys.arena, "{\"name\":\"box\"}")
    _ = Item.FromJson(sys.arena, &w) else e {
        try sys.Print("{e}\n", (e))
    }
}
```

```text
$ mk run item.mk
{"name":"dot","count":3,"shape":{"Circle":{"r":1.5}},"note":null,"tags":["red"]}
box x7, note is none: true
Mismatch(offset: -1)
```

- `Shape` has its own `gen` line because `Item` has a `Shape` field.
- `FormatJson` writes `none` as `null`; `FromJson` reads a missing member and `null` alike as `none`.

## API

<!-- api:begin -->
Stability: Stable.

### `Codec`

```
export Codec(g: mut Gen, t: Type)
```

Codec writes, for the struct or enum t, `impl json.Json for T`, whose
FormatJson writes T as compact JSON, and `T.FromJson(a, v)`, which reads
T back from a parsed Value. Use it as `gen json.Codec(T)`.

A struct is an object of its exported fields, in declaration order,
keyed by field name. FromJson ignores unknown members, takes the last of
duplicates, and fails with Mismatch on a missing field that is not ?T. A
?T is the value or null, and a missing member reads as none. str, bool,
the integers, f32, f64, []T and List[T] have their JSON forms; a NaN or
an infinity is written as null, which FromJson then refuses. A field of
another struct or enum type uses that type's own FormatJson and
FromJson, so it needs its own gen line (E0905 in the emitted code
without one). An enum is tagged by variant name: a variant with no
payload is the string "V", one with a payload {"V":{fields}}.

E0903 at a field that is not exported or whose type has no JSON form
(char, [N]T, references, json.Value). Runs at compile time; the emitted
FromJson allocates in its arena, FormatJson nothing. FromJson copies
every string it reads into a, so its result borrows a alone (`from a`)
and the parsed Value may end before it.

### `Depth`

```
export error Depth(offset: int)
```

The text nests arrays and objects more than 512 deep; offset is the
opening bracket of the 513th.

### `Error`

```
export open type Error = Syntax | Mismatch | Depth | OutOfMemory
```

Every way Parse and FromJson fail. Parse takes a str, which is valid
UTF-8, and refuses an escape that decodes to a lone surrogate with
Syntax at the escape, so the set has no Utf8Error.

### `Json`

```
export interface Json {
    FormatJson(&self, w: mut Writer): WriteError!void
}
```

Json is what gen json.Codec(T) implements: FormatJson writes T as compact
JSON, so generic code can encode any codec type.

### `Member`

```
export struct Member
```

One member of an Object: its key and its value.

Fields:

- `key: str`: The member's key.
- `value: Value`: The member's value.

### `Mismatch`

```
export error Mismatch(offset: int)
```

A value does not have the shape FromJson reads; offset is -1, since a
Value keeps no offsets.

### `Parse`

```
export Parse(a: &Arena, text: str into a): Error!Value from a, text
```

- Flows: The result borrows from a and text.
- Errors: Error (OutOfMemory, Depth(offset: int), Mismatch(offset: int), Syntax(offset: int))

Parses RFC 8259 text into a Value in a. The grammar is exact: no
comments, no trailing commas, no leading zeros, no + sign, no bare
control characters in a string. Fails with Syntax at the first byte that
cannot continue the text, Depth past 512 levels of nesting, OutOfMemory
from a. O(text); allocates the List buffers of arrays and objects and
the decoded copy of each escaped string in a, nothing for a string
without an escape. A string without an escape is a view of text, so text
is stored into a.

Each level of nesting is a recursive call. A dev build's frames are
larger, so on a task's default stack a dev build reaches the stack limit
(a panic) before 512 levels, while a release build fails with Depth
(measured in a dev-built server on x86-64 Windows: 400 levels parse, a
512-deep body panics).
Parse untrusted text in a release build, or bound its depth first.

### `Syntax`

```
export error Syntax(offset: int)
```

The text is not RFC 8259 JSON; offset is the first byte that cannot
continue it (the text's length when it ends too soon).

### `Value`

```
export enum Value
```

A JSON value, a tree in the caller's arena. Number keeps its text, and
Text is a view of the parsed text unless the string held an escape. A
Value formats as compact JSON, so `{v}` in a Print writes it; a Number
whose text is not a JSON number fails with WriteError.

Variants:

- `Null`: null.
- `Bool(b: bool)`: true or false.
- `Number(text: str)`: A number, kept as its text.
- `Text(s: str)`: A string, its escapes decoded.
- `Array(items: List[Value])`: An array of values.
- `Object(members: List[Member])`: An object's members.

### `Value.Float`

```
export Value.Float(&self): ?f64
```

Float is a Number's value as the nearest f64, correctly rounded; a
magnitude past f64's range gives none, a total underflow a zero with the
sign. None for any other variant. O(text); allocates nothing.

### `Value.Get`

```
export Value.Get(&self, key: str): ?&Value
```

- Flows: The result borrows from self.

Get is the value of the last member named key, when self is an Object;
none when there is no such member or self is not an Object. RFC 8259
leaves duplicate names to the reader; here the last one wins.
O(members), a scan from the end; allocates nothing.

### `Value.Int`

```
export Value.Int(&self): ?i64
```

Int is a Number whose text is an integer (no fraction or exponent) that
fits i64: "1.0" and "1e2" give none. None for any other variant.
O(text); allocates nothing.

### `Value.Str`

```
export Value.Str(&self): ?str
```

- Flows: The result borrows from self.

Str is a Text's string, a view of the parsed text or of the arena; none
for any other variant. O(1); allocates nothing.

<!-- api:end -->
