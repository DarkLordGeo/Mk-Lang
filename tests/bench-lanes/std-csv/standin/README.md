# std-csv stand-ins (not a row)

A labelled table would set std.csv against Rust's `csv` crate, Go's `encoding/csv` and a C reader. The idiomatic table compares standard libraries and admits no stand-in (tests/bench/README.md, "No stand-ins"), so the C reader and the Rust crate cannot sit in `../read`. This directory holds the C reader until `tools/bench` gives such cells a labelled table of their own; it has no `bench.json`, so `tools/bench` runs nothing here.

- `csv-read.c`: a hand-written RFC 4180 state machine over `fread` (64 KiB blocks), csv-core's DFA shape. It counts each field's decoded length and builds no field value, so it does less work than the Mk and Go cells, and it folds a CR LF inside quotes to LF, which std.csv does not do. A labelled row must say both.
