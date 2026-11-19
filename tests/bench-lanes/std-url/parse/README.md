# url-parse (idiomatic table)

Task: `url-parse <n> <rounds>`. Make n URLs, then rounds times parse each one and add to a wrapping `u64` sum the lengths of its scheme, host, path, query and fragment, and its port (0 for none). The program prints the sum.

Generator (splitmix64, seed 1; `next()` is one output, `pick(k)` is `next() % k`): for each URL, `z = next()`; `upper` is `(z >> 44) % 8 == 0`; the scheme is `["http","https","ws","wss","ftp"][z % 5]`, in upper case when `upper`, and `://`; the host is, when `(z >> 47) % 8 == 0`, the IPv4 address `10.x.y.w` with `x`, `y`, `w` drawn in that order as `pick(256)` and written in decimal, else `1 + (z >> 16) % 3` labels, each `3 + pick(8)` characters of `a-z0-9` (each `alnum[pick(36)]`, the same draw read from `A-Z0-9` for the first label when `upper`) and a `.`, then the TLD `["com","org","net","dev"][(z >> 24) % 4]`; the port `[0, 8080, 8443, 3000][(z >> 8) % 4]` as `:port` unless 0; `/` and `(z >> 32) % 6` segments joined by `/`, each `1 + pick(12)` characters of `a-z0-9-_~`, and a trailing `/` when there are segments and bit 40 of `z` is set; when bit 41 is set, `?` and `1 + pick(3)` pairs joined by `&`, each `1 + pick(6)` characters of `a-z0-9`, `=`, and `pick(8)` more; when `(z >> 42) & 3` is 0, `#` and `1 + pick(10)` characters of `a-z0-9`.

So about one URL in eight has an upper-case scheme and first label and one in eight an IP host: input every parser reads alike, which Mk's one-copy path (`SerializeSame`, `IsPlainDomain`) does not take, so the row measures both of `url.Parse`'s paths. Go and Zig do the same work on either form.

Checksums: quick (`1000 10`) `48960600`, publish (`100000 20`) `9878832700`.

Columns: Mk `url.Parse` (WHATWG URL Standard, a fresh arena each round, freed at its end); Go `net/url.Parse`; Zig `std.Uri.parse`. C and Rust have no URL parser in their standard libraries.
