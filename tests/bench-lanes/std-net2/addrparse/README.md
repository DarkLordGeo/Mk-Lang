# addrparse (std-net2, idiomatic table)

Each of `n` rounds draws `x` from splitmix64 (seed 1). An even `x` is an IPv4 address of the bytes `x >> 8`, `x >> 16`, `x >> 24`, `x >> 32` (low 8 bits each); an odd `x` draws `y` and `z` and is an IPv6 address whose group `k` (0 to 7, most significant first) is the 16 bits `(k < 4 ? y : z) >> (16 * (k % 4))`, set to 0 when bit `k` of `(x >> 8) & (x >> 16)` is 1 (zero runs of every length), and group 0 or-ed with `0x2000` (never zero, so no address is IPv4-mapped or IPv4-compatible). The port is `(x >> 40) & 0xFFFF`. The address and port are formatted with the standard library's text form (`a.b.c.d:port`, `[v6]:port`, RFC 5952) into a reused buffer, then parsed back with the standard library's parser. `sum = 31 sum + port`, then `31 sum + b` for every address byte of the parsed value (4 for IPv4, 16 for IPv6), wrapping. Prints `n sum`.

| Language | Format | Parse |
|---|---|---|
| Mk | `impl Format for net.Addr` into a `Text` | `net.ParseAddr` |
| Go | `netip.AddrPort.String` | `netip.ParseAddrPort` |
| Rust | `write!` of `SocketAddr` into a reused `String` | `str::parse::<SocketAddr>` |
| C | `inet_ntop` plus a hand-written `:port` and brackets | `inet_pton` after a hand-written split and port loop (C has no ip:port facility) |
| Zig | `std.Io.net.IpAddress.format` into a fixed `std.Io.Writer` | `IpAddress.parseLiteral` |

The text's length is not in the checksum: with it, the C column's checksum differed from the other four (`tools/bench verify -publish-size`, 2026-11-07), so Windows' `inet_ntop` writes some IPv6 addresses other than as RFC 5952 does (Mk, Go and Rust agreed). The round trip itself is checked in every column.
