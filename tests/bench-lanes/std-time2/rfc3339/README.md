# time-rfc3339 (idiomatic table)

Make n instants of 1970 to 2099 (splitmix64, seed 1, with nanoseconds), then rounds times write each as RFC 3339 in UTC with its fraction (Go's RFC3339Nano) and parse the text back; the checksum adds per instant and round the parsed nanoseconds and the text's length, wrapping.

Generator: splitmix64 from state 1 (`next()` adds 0x9E3779B97F4A7C15 to the state and mixes it with the shifts 30, 27 and 31 and the multipliers 0xBF58476D1CE4E5B9 and 0x94D049BB133111EB).

Columns: Mk and Go; `bench.json`'s `skip` says why C, Rust and Zig have none.

Not published: `tools/bench publish` writes the numbers (bench8).
