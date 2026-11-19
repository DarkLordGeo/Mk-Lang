# regexrare (idiomatic table)

Find of two patterns whose literal prefix is rare in the text, so the scan spends its time in the prefilter that skips to the next possible start: `#[0-9]+` (a one-byte prefix; Mk `simd.IndexByte`) and `needle[0-9]+` (a six-byte prefix; Mk the vector pair filter of `FindPre`, std/regex/dfa.mk), each planted once 100 bytes before the end of `mib` MiB of log-like text from splitmix64(`seed`), the same bytes in every language. The rows of tests/bench-lanes/std-regex have frequent or no prefixes, where the prefilter does not show.

Mk `regex.Compile` and `Regex.Find`; Go `regexp.MustCompile` and `FindIndex` (testdata/regexrare.go). C, Rust and Zig have no std regex. The row also serves `go run ./tools/bench compare --base <mk before> --head <mk after> -cols mk std-vector/regexrare-compile std-vector/regexrare-gen`.

Variant `gen` (fourth argument `gen`) runs the same two patterns as `gen regex.Matcher` code, whose emitted prefilter `RxPre` is dfa.mk's `FindPre` written out; Go has no build-time regex, so its cell there runs the same compiled regexps as in variant `compile`. Variant `compile` is the plain row.

Variants `eng` and `enggen` draw the text's letters in English frequency proportions (`Eng` in mk/regexrare.mk, 104 letters of 128 draws, then spaces, three digits and a newline) and look for `theseus[0-9]+` and `needle[0-9]+`, planted at the same places: their first and last bytes are common in such text (t and s, n and e), so a pair filter on them keeps many false candidates, while the rarest pair by `ByteRank` (h and u, d and l) keeps few.
