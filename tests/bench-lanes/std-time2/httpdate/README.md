# time-httpdate (idiomatic table)

Make n whole-second instants of 1970 to 2099 (splitmix64, seed 1), then rounds times write each as an IMF-fixdate (RFC 9110 5.6.7, "Sun, 06 Nov 1994 08:49:37 GMT") and parse the text back; the checksum adds per instant and round the parsed seconds and the text's length, wrapping.

Generator: splitmix64 from state 1 (`next()` adds 0x9E3779B97F4A7C15 to the state and mixes it with the shifts 30, 27 and 31 and the multipliers 0xBF58476D1CE4E5B9 and 0x94D049BB133111EB).

Columns: Mk and Go; `bench.json`'s `skip` says why C, Rust and Zig have none.

Not published: `tools/bench publish` writes the numbers (bench8).
