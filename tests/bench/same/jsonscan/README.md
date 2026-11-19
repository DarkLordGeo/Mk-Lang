# jsonscan

The `json-scan` row (named `jsonscan`: a Mk package name cannot hold a hyphen): a JSON tokenizer, a byte loop with a branch per byte class and short inner loops over strings and numbers. It builds no tree and allocates nothing; the idiomatic parse-and-serialise row is `json`. Overflow class `wraps`: the token hash wraps by design, so every column uses its wrapping operators for it (C `uint64_t`, Rust `wrapping_*`, Zig `+%` and `*%`, Go plain, Mk `+%` and `*%`); indices are signed 64-bit (C `int64_t`, Go and Mk `int`; Rust and Zig `usize`, the type they index with), the string length `l` is signed 64-bit in every column (C `int64_t`, Rust and Zig `i64`, Go and Mk `int`) and enters the hash as its 64 bits (`(uint64_t)l`, `l as u64`, `@bitCast`, `uint64(l)`, `Trunc[u64](l)`), and neither overflows.

**Input.** `tools/bench gen` writes `tests/bench/data/jsonscan.json` from the row's `data` spec (`kind` json, about 32,000,000 bytes, seed 1): one JSON array of records, separated by `,\n`, each `{"id":<int>,"name":<string>,"score":[-]<int>.<3 digits>,"tags":[<0 to 4 strings>],"ok":true|false,"next":null|<record>}` with `next` nested at most three deep, every choice drawn from splitmix64 (`genJSON` in `tools/bench/gen.go`). Strings are 1 to 24 printable ASCII characters with `\"` and `\\` escapes. The program reads the file once into memory (outside the scan, a few milliseconds of the run) and scans it `rounds` times.

```
jsonscan(path, rounds):
    b = the bytes of the file; n = len(b)
    total = 0
    for r in 0 .. rounds-1:
        total = scan(b, total)             each round starts from the last, so no round can be hoisted
    print total                             as an unsigned decimal

scan(b, h):                                 h is u64, every * and + on it wraps
    i = 0
    while i < n:
        c = b[i]
        if c is ' ', '\n', '\r' or '\t':     i = i + 1
        else if c == '"':
            i = i + 1; l = 0
            while i < n and b[i] != '"':
                if b[i] == '\\': i = i + 1   an escape is one character
                i = i + 1; l = l + 1
            i = i + 1
            h = h*31 + l                     a string hashes as its decoded length
        else if c == '-' or c is a digit:
            neg = 0
            if c == '-': neg = 1; i = i + 1
            v = 0
            while i < n and (b[i] is a digit or b[i] == '.'):
                if b[i] != '.': v = v*10 + (b[i] - '0')
                i = i + 1
            h = h*31 + (v*2 + neg)           123.456 hashes as 123456*2, negative adds 1
        else if c == 't': h = h*31 + 1; i = i + 4
        else if c == 'f': h = h*31 + 2; i = i + 5
        else if c == 'n': h = h*31 + 3; i = i + 4
        else:             h = h*31 + c; i = i + 1      { } [ ] : ,
    return h
```

Every byte compare is against a constant; the `if` chain is written in this order in every column (a `switch` would let some compilers build a jump table and others not, so no column uses one). Bounds: every `b[i]` is checked in Mk, Rust, Zig ReleaseSafe and Go, and unchecked in C and Zig ReleaseFast. The `i < n` tests are part of the algorithm and are the same in every column.

Sizes: publish `{data} 52`, quick `{data} 1`; `{data}` is replaced by the data file's absolute path. Checksums are in `bench.json`.
