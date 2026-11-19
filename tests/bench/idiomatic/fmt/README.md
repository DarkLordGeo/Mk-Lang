# fmt (idiomatic)

**Task.** Format and parse back 10 M integers and 10 M floats with each language's standard formatting and parsing. `fmt <n> <rounds> <seed>`:

```
st = seed; sum = 0; ok = 0                       (i64, wrapping)
rounds times, n times:
    st = st + G; z = mix(st)
    x = i64(z >> 1), negated when z & 1            (any i64 but the minimum)
    st = st + G
    f = f64(mix(st) >> 11) * 2^-53 * 1e6           (53 random bits, in [0, 1e6))
    y = parse_int(format(x))
    g = parse_float(format(f))
    sum = sum + y + i64(trunc(g))
    if g == f: ok = ok + 1
print sum + ok
```

**Checksum.** It is taken over the parsed-back values, never over the output bytes: every format here round-trips, so `y == x` and `g == f`, and the line is the same whatever digits a library prints. `ok` counts the floats that came back bit-exact, so a format that does not round-trip changes it.

**Cells.**

| Column | Integer | Float |
|---|---|---|
| mk | `Print(mut w, "{x}", (x))` into a `Text`'s `Writer`, `str.ParseInt` | `"{f}"`: the shortest round-trip digits, `str.ParseFloat` |
| c | `snprintf("%lld")`, `strtoll` | `snprintf("%.17g")`, `strtod`: **not shortest**, C's std has no shortest format, so it prints 17 significant digits (more bytes, not the same work as the other columns) |
| rust, rust-ovf | `write!(s, "{}")` into a `String`, `str::parse` | `Display`: shortest round-trip |
| zig-fast, zig-safe | `std.fmt.bufPrint("{d}")`, `std.fmt.parseInt` | `"{d}"`: shortest round-trip, `std.fmt.parseFloat` |
| go | `strconv.AppendInt`, `strconv.ParseInt` | `strconv.AppendFloat(.., 'g', -1, 64)`: shortest, `strconv.ParseFloat` |

Sizes: quick `100000 1 1`, publish `10000000 2 1` (the fastest cell, Zig ReleaseFast, ran one round in 0.87 s in a verify run on a shared machine, 2026-11-07). Overflow class `wraps`.
