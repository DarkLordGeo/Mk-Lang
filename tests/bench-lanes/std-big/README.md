# std-big benchmark rows (idiomatic standard library)

Rows of std.big for the idiomatic table, under tests/bench/README.md's contract: each language uses its standard library's big integers, and a language with none says so in `skip`. Inputs are splitmix64 (Steele, Lea and Flood 2014) from the seed in argv; every program prints one checksum line. Only `tools/bench` publishes a number from these rows.

| Row | Work | Mk | Baselines |
|---|---|---|---|
| `modpow` | n steps of x = x^e mod m at 2048 bits, the result copied out of the arena through `Words` each step | `big.Int.ModPow` | Go `math/big` `Exp` (Zig's std has no modular power) |
| `factorial` | n - 1 products of a growing value by one word, copied out each step, then the decimal text | `big.Int.Mul`, `Text` | Go `math/big` `Mul`, `Text`; Zig `std.math.big.int` `mul`, `toString` |
| `muldiv` | n steps of x = (x y mod d) + (x y div d), 1024 by 512 words, then 1536 by 1024, copied out each step | `big.Int.Mul`, `DivMod`, `Add` | Go `Mul`, `QuoRem`, `Add`; Zig `mul`, `divTrunc`, `add` |
| `conv` | n steps of x = Parse(Text(x, 10)) + 1 at 2000 words (about 38,500 digits), copied out each step | `big.Int.Text`, `big.Parse` | Go `Text`, `SetString`; Zig `toString`, `setString` |

Rust's num-bigint and GMP are the usual comparisons; neither is a standard library, and neither is on this machine, so the C and Rust columns are skipped with that reason.
