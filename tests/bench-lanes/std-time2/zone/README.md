# time-zone (idiomatic table)

Make n instants of 1970 to 2099 (splitmix64, seed 1: whole seconds below 4,102,444,800, then nanoseconds below 10^9), load America/New_York from each library's embedded zone database, then rounds times take each instant's civil fields in the zone; the checksum adds per instant and round the year, month, day, hour, minute, second and the offset plus 86,400, wrapping.

Generator: splitmix64 from state 1 (`next()` adds 0x9E3779B97F4A7C15 to the state and mixes it with the shifts 30, 27 and 31 and the multipliers 0xBF58476D1CE4E5B9 and 0x94D049BB133111EB).

Columns: Mk and Go; `bench.json`'s `skip` says why C, Rust and Zig have none.

Not published: `tools/bench publish` writes the numbers (bench8).
