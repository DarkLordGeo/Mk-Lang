# url-query (idiomatic table)

Task: `url-query <n> <rounds>`. Make n application/x-www-form-urlencoded queries, then rounds times parse each one and add to a wrapping `u64` sum the length of every name and every value (after decoding). The program prints the sum.

Generator (splitmix64, seed 1; `next()` is one output, `pick(k)` is `next() % k`): for each query, `1 + pick(8)` pairs joined by `&`; for each pair `z = next()`, the name is `1 + (z >> 8) % 8` pieces, then `=`, then the value is `(z >> 16) % 12` pieces; each piece is `pieces[pick(48)]` when `z % 4 == 0` (a pair to decode) and `pieces[pick(40)]` otherwise, where `pieces` is the 40 characters `a-z0-9-._~` followed by `+`, `+`, `+`, `+`, `%20`, `%2B`, `%C3%A9`, `%26`.

So about three pairs in four have nothing to decode, and the rest have spaces as `+` and escapes of whole UTF-8 sequences; no query holds `;` or a malformed escape, so Go's `net/url.ParseQuery` and the URL Standard's parser give the same pairs, and the checksum, a sum, does not depend on Go's map order.

Checksums: quick (`1000 10`) `457110`, publish (`100000 20`) `90669740`.

Columns: Mk `url.ParseQuery` (WHATWG URL Standard section 5.1, a fresh arena each round, freed at its end); Go `net/url.ParseQuery`. C, Rust and Zig have no form-urlencoded parser in their standard libraries.
