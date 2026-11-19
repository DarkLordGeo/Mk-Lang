# time-duration (idiomatic table)

Make n durations (splitmix64, seed 1: each the next output as a signed 64-bit value, shifted right arithmetically by the next output modulo 64, so every magnitude occurs), then rounds times write each in Go's Duration.String form ("1h2m3.5s") and parse the text back; the checksum adds per duration and round the parsed nanoseconds with the sign bit cleared and the text's length, wrapping.

Generator: splitmix64 from state 1 (`next()` adds 0x9E3779B97F4A7C15 to the state and mixes it with the shifts 30, 27 and 31 and the multipliers 0xBF58476D1CE4E5B9 and 0x94D049BB133111EB).

Columns: Mk and Go; `bench.json`'s `skip` says why C, Rust and Zig have none.

Not published: `tools/bench publish` writes the numbers (bench8).
