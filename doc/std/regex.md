# std.regex

`std.regex` matches regular expressions in RE2's syntax, without back-references, in time linear in the input whatever the pattern. A pattern from outside the program, such as a search box or a configuration file, cannot make a match run for long. Use `Compile` for a pattern known only at run time, and `gen regex.Matcher` for one fixed in the source, which compiles when the program builds. For plain substring search, `str.Find` and its relatives are enough.

Read the [library guide](README.md) first. The cookbook has a recipe: [Filter lines with a regular expression](../cookbook.md#filter-lines-with-a-regular-expression).

```mk
// verdict: parse
import std.regex
```

## Contents

- [The main pieces](#the-main-pieces)
- [Syntax](#syntax)
- [Compiling and matching](#compiling-and-matching)
- [Groups and replacement](#groups-and-replacement)
- [Patterns fixed at build time](#patterns-fixed-at-build-time)
- [Errors](#errors)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [find and capture](#find-and-capture), [find all and replace](#find-all-and-replace), [match at build time](#match-at-build-time)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Compile a pattern | `Compile`, which returns a `Regex` |
| Is there a match | `Regex.IsMatch` |
| Find the first match | `Regex.Find`, which returns a `Match` |
| The text of a group | `Regex.Group` |
| Every match | `Regex.FindAll` |
| Replace every match | `Regex.Replace` |
| A pattern compiled at build time | `gen regex.Matcher(T, "pattern")`, then `T.IsMatch`, `T.Find` |

## Syntax

The syntax is RE2's without back-references:

| Form | Meaning |
|---|---|
| `.` `[abc]` `[^a-z]` `[[:alpha:]]` | any character (not `\n` unless `s`), classes, ASCII classes |
| `x\|y` | either; the leftmost alternative is preferred |
| `\d` `\w` `\s` and `\D` `\W` `\S` | Perl classes (ASCII) |
| `\pL` `\p{Lu}` `\p{Greek}` `\PL` | a Unicode general category or script, and its complement |
| `*` `+` `?` `{n}` `{n,}` `{n,m}` | repeats, greedy; add `?` for lazy. A count is at most 1,000, and so is the product of nested counts: `(?:a{10}){101}` is `Syntax` |
| `(re)` `(?P<name>re)` `(?<name>re)` `(?:re)` | a group, a named group, a group that does not capture |
| `^` `$` `\A` `\z` `\b` `\B` | text or line start and end, word boundary (ASCII word characters) and its opposite |
| `(?i)` `(?m)` `(?s)` `(?U)` | flags: ignore case, `^` and `$` at lines, `.` matches `\n`, repeats lazy by default |
| `(?flags:re)` `(?-flags)` | flags for one group only, or cleared |
| `\x41` `\x{10FFFF}` `\101` | a character by its code, in hex or octal |
| `\Q...\E` | literal text |
| `\.` `\*` `\n` `\t` | an escaped metacharacter or control character |

`(?i)` folds case by Unicode simple case folding. Back-references (`\1`) and look-around (`(?=...)`) are `Syntax` errors. Write patterns as raw strings, `r"\d+"`, so the backslashes reach `Compile` as they are.

## Compiling and matching

`Compile(a, pattern)` parses the pattern and builds two DFAs from it at once, one forward and one in reverse. Each is built under a budget of 4,096 states and of build work, so `Compile` itself takes bounded time. A pattern whose DFA would pass the budget is matched by a Pike VM instead, still in linear time. `IsMatch`, `Find` and `Group` allocate nothing on either path, and a `Regex` is `Share`: compile it once and use it from many tasks.

```mk
// verdict: ok
import std.regex

// FirstNumber is the first run of digits in s. It compiles the pattern on each
// call; compile once and keep the Regex when you match often.
FirstNumber(a: &Arena, s: str): regex.Error!?str from s {
    re := try regex.Compile(a, "[0-9]+")
    m := re.Find(s) else { return none }
    return re.Group(s, m, 0)
}
```

- Matches are leftmost-first: of the matches that start leftmost, the one the pattern's alternatives and repeats prefer.
- A `Match` is the byte offsets `lo` and `hi` of the matched text `s[lo..hi]`, always on character boundaries.
- `IsMatch` reports whether any substring matches, and `Find` returns the leftmost match or `none`.
- `FindAll` returns the successive non-overlapping matches, each search starting where the last match ended. An empty match right after a match is skipped, and an empty match moves the next search on by one character.

## Groups and replacement

`Group(s, m, i)` is group `i`'s text in the match `m` of `s`. Group 0 is the match itself. It is `none` for a group that took no part in the match, for an `i` that names no group, and for an `m` that was not found in `s`. `Group` takes an index: named groups are numbered with the rest, and a name is used only by `Replace`'s `$name`.

`Replace(a, s, with)` replaces every match, as `FindAll` finds them, with `with`, expanded:

| In `with` | Becomes |
|---|---|
| `$1`, `${1}` | group 1's text |
| `$name`, `${name}` | the named group's text |
| `$$` | `$` |

In `$name` the name is the longest run of letters, digits and `_`, so write `${1}x` or `${name}x` to put text right after a group. A group that does not exist or took no part expands to nothing. The result is a new `str` in `a`, also when nothing matched.

## Patterns fixed at build time

`gen regex.Matcher(T, "pattern")`, usually on an empty struct `T`, compiles the pattern when the program builds and writes `T.IsMatch(s: str): bool` and `T.Find(s: str): ?Match` as table-driven code. They need no arena and no `Compile` at run time, and run in O(n). The generator runs under the `gen` line's compile-time [fuel](../reference.md#fuel). A long or repetitive pattern such as `a{100}` runs out of it, which is E0902 at the `gen` line: add `fuel 100_000_000` after the line's `)`, or use `Compile`. A bad pattern, or one whose DFA needs more than 512 states, is E0903, reported at `T`; use `Compile` for the second.

## Errors

| Member | Meaning |
|---|---|
| `Syntax(offset)` | The pattern is not valid; `offset` is the pattern byte where the error is found |
| `TooLarge` | The pattern nests more than 1,000 levels, or it compiles to more than 4,096 instructions, as `\pL{100}` does |
| `OutOfMemory` | The arena is full |

`regex.Error` is the union, and only `Compile` returns it. `FindAll` and `Replace` fail only with `OutOfMemory`; `IsMatch`, `Find` and `Group` cannot fail. `regex.Error` is open, so a `match` on it outside std keeps a `_` arm.

## Memory and cost

| Call | Cost | Allocates |
|---|---|---|
| `Compile` | O(pattern) to parse, O(states × 256) for each DFA | the program, the DFA tables (at most 4,112 KiB each), the group names, in `a` |
| `IsMatch`, `Find` | O(n), or O(n × program) on the Pike VM | nothing |
| `Group` | one pass over the match | nothing |
| `FindAll` | O(n) per match found | the `List` of matches, in `a` |
| `Replace` | `FindAll`'s cost, plus a pass over each match for its groups | the result and scratch for the groups, in `a` |

`Compile`'s parse tree and other scratch go in a sub-arena of `a` that is freed before it returns, so `a` keeps only what matching reads. Nothing in `std.regex` takes an `Io`, so nothing here is cancelled.

## Examples

Each program below runs as written with `mk run`.

### Find and capture

```mk
// verdict: ok
import std.regex

export Main(sys: &System): regex.Error|WriteError!void {
    // Compile once; matching allocates nothing.
    re := try regex.Compile(sys.arena, r"(?P<key>\w+)=(?P<value>[^;]*)") // r"..." has no escapes
    s := "user=ada; lang=mk"
    m := re.Find(s) else { return } // the leftmost match
    try sys.Print("match {lo}..{hi}: {text}\n", (lo: m.lo, hi: m.hi, text: s[m.lo..m.hi]))
    key := re.Group(s, m, 1) else { "" }
    value := re.Group(s, m, 2) else { "" }
    try sys.Print("key {key}, value {value}\n", (key, value))

    digit := try regex.Compile(sys.arena, r"\d")
    try sys.Print("has a digit: {d}\n", (d: digit.IsMatch(s)))

    // A bad pattern fails with the byte offset of the error.
    _ = regex.Compile(sys.arena, "a(b") else e {
        try sys.Print("{e}\n", (e))
    }
}
```

```text
$ mk run find.mk
match 0..8: user=ada
key user, value ada
has a digit: false
Syntax(offset: 1)
```

- Named groups are numbered too, from left to right: `key` is group 1.
- The error offset 1 is the `(` that is never closed.

### Find all and replace

```mk
// verdict: ok
import std.regex

export Main(sys: &System): regex.Error|WriteError!void {
    re := try regex.Compile(sys.arena, r"(\d{4})-(\d{2})-(\d{2})")
    s := "from 2026-01-31 to 2026-03-01"

    all := try re.FindAll(sys.arena, s) // non-overlapping, left to right
    for m in all.Items() {
        try sys.Print("found {d}\n", (d: s[m.lo..m.hi]))
    }

    // $1 and ${name} expand to a group's text; $$ is a dollar sign.
    us := try re.Replace(sys.arena, s, "$2/$3/$1")
    try sys.Print("{us}\n", (us))

    // (?i) matches without case; \p{Greek} is a Unicode script.
    word := try regex.Compile(sys.arena, r"(?i)\bmk\b")
    greek := try regex.Compile(sys.arena, r"\p{Greek}+")
    t := "MK and αβγ"
    try sys.Print("{a} {b}\n", (a: word.IsMatch(t), b: greek.Group(t, greek.Find(t) else { return }, 0) else { "" }))
}
```

```text
$ mk run replace.mk
found 2026-01-31
found 2026-03-01
from 01/31/2026 to 03/01/2026
true αβγ
```

- `Replace` keeps the text between matches as it is.
- `αβγ` is six bytes; the match's offsets fall on character boundaries.

### Match at build time

```mk
// verdict: ok
import std.regex

struct Hex {}

gen regex.Matcher(Hex, "^#[0-9a-fA-F]{6}$") // compiled when the program builds

export Main(sys: &System): WriteError!void {
    for s in ["#1e90ff", "#12345", "red"] {
        try sys.Print("{s}: {ok}\n", (s, ok: Hex.IsMatch(s))) // no arena, no Compile
    }
}
```

```text
$ mk run hex.mk
#1e90ff: true
#12345: false
red: false
```

- A bad pattern is error E0903 at `T` when the program builds, not a failure when it runs.

## API

<!-- api:begin -->
Stability: Stable.

### `Compile`

```
export Compile(a: &Arena, pattern: str): Error!Regex from a
```

- Flows: The result borrows from a.
- Errors: Error (OutOfMemory, Syntax(offset: int), TooLarge)

Compiles pattern into a Regex in a. The syntax is RE2's without
back-references: Perl classes, \p{..} Unicode classes, named groups,
lazy repeats and the flags i, m, s and U; matching is leftmost-first.
Fails with Syntax at the first bad byte, TooLarge past 1,000 levels of
nesting or 4,096 instructions, or OutOfMemory.

The forward and reverse DFAs are built at once, under a budget of 4,096
states and of build work each; past the budget, matching runs the Pike
VM instead.
O(pattern) to parse and compile, O(states x 256) per DFA. Keeps in a
only what matching reads: the program and each DFA's tables at their
exact sizes (at most 4096 x 257 cells of 4 bytes, 4,112 KiB, a DFA),
the group names and the literal prefix. The parse tree and the build's
scratch go in a sub-arena of a, freed before it returns.

### `Error`

```
export open type Error = Syntax | TooLarge | OutOfMemory
```

Every way Compile fails.

### `Match`

```
export struct Match
```

A match: the byte offsets lo to hi of s, on scalar boundaries; the
matched text is s[lo..hi].

Fields:

- `lo: int`: The byte offset of the match's start in s.
- `hi: int`: The byte offset where the match ends in s, exclusive.

### `Matcher`

```
export Matcher(g: mut Gen, t: Type, pattern: str)
```

Matcher writes, for pattern, `T.IsMatch(s: str): bool` and
`T.Find(s: str): ?Match` as table-driven code: the pattern compiles when
the program builds, and matching needs no arena and no Compile at run
time. Use it as `gen regex.Matcher(T, "pattern")`, usually on an empty
struct T. Fails with E0903 at T when the pattern is not valid or either
of its DFAs needs more than 512 states; use Compile for such a pattern.
A long pattern can run out of the gen line's fuel first, E0902: state
more with `fuel N` on the line. Runs at compile time; the emitted
functions are O(n) and allocate nothing.

### `Regex`

```
export view struct Regex
```

Regex is a compiled pattern. It lives in the arena Compile was given,
and matching writes nothing but its own frame and the arena a call is
given, so one Regex can be used by many tasks at once (it is Share).

### `Regex.Find`

```
export Regex.Find(&self, s: str): ?Match
```

Find gives the leftmost-first match, or none. O(n) with the DFAs, O(n x
program) with the Pike VM; allocates nothing.

### `Regex.FindAll`

```
export Regex.FindAll(&self, a: &Arena, s: str): OutOfMemory!List[Match] from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

FindAll gives the successive non-overlapping matches, each search
starting where the last match ended. An empty match right after a match
is skipped, and an empty match moves the next search on by one scalar.
Fails only with OutOfMemory. O(n) per match found with the DFAs;
allocates the List in a.

### `Regex.Group`

```
export Regex.Group(&self, s: str, m: Match, i: int): ?str from s
```

- Flows: The result borrows from s.

Group gives group i's text in the match m of s: i == 0 is the match
itself; for a group, the text it took in the leftmost-first match that
starts at m.lo, if that match ends at m.hi. none for a group that took
no part, for an i that names no group, or for an m outside s. One
anchored run over the match; allocates nothing.

### `Regex.IsMatch`

```
export Regex.IsMatch(&self, s: str): bool
```

Reports whether some substring of s matches. O(n) with the DFAs, O(n x
program) with the Pike VM; allocates nothing.

### `Regex.Replace`

```
export Regex.Replace(&self, a: &Arena, s: str, with: str): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

Replace is s with every match replaced by with, in which $1, ${1},
$name and ${name} give a group's text (nothing for a group that took no
part or does not exist), and $$ is $. In $name the name is the longest
run of letters, decimal digits and _, of any script; write ${name} to
end it early. The result is a new str in a, also when nothing matched.
Fails only with OutOfMemory. FindAll's cost plus one capture run per
match; allocates the result and the capture rows in a.

### `Syntax`

```
export error Syntax(offset: int)
```

The pattern is not valid syntax; offset is the pattern byte where the
error is found.

### `TooLarge`

```
export error TooLarge
```

The pattern nests more than 1,000 levels, or its program passes 4,096
instructions.

<!-- api:end -->
