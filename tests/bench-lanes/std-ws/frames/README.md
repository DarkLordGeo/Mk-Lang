# ws-frames (same-algorithm table)

Task: `ws-frames <batches> <rounds>`. std.websocket's receive path, written once per language with the same data structures and loops, so a row measures `Conn.Recv`'s path.

Input, built once: `batches` copies of one batch of seven messages, as a client sends them (every frame masked):

| Message | Opcode | Bytes | Frames |
|---|---|---|---|
| 1 | binary | 0 | 1 |
| 2 | binary | 125 | 1 (7-bit length) |
| 3 | text | 126 | 1 (16-bit length) |
| 4 | binary | 65,535 | 1 |
| 5 | binary | 70,000 | 1 (64-bit length) |
| 6 | text | 30,000 | 3, of 9,999, 10,001 and 10,000 bytes, the first cut inside a 4-byte character |
| 7 | text | 1,000 | 1, its last byte 0xFF, so it is not UTF-8 |

Binary payloads are the first bytes of 70,000 bytes of splitmix64 output (seed 1, as `ws-mask`). Text payloads are the first bytes of the 10-byte unit `61 C3 A9 E2 82 AC F0 9F 98 80` ("a", U+00E9, U+20AC, U+1F600) repeated. Frame `f` (its index in the whole buffer, from 0) is masked with the four low bytes of `(f + 1) * 0x9e3779b97f4a7c15`, little-endian.

Per pass `r` in `0..rounds`, over the whole buffer: parse each header (FIN, reserved bits, opcode, mask bit, a 7-, 16- or 64-bit length, the key); check the reserved bits are 0, the mask bit is set, the payload fits the buffer and the message fits the 70,000-byte message buffer, else add 1000003 and end the pass; unmask the payload a byte at a time (`msg[mlen + i] = buf[pos + i] ^ key[i & 3]`) after what the message holds; at the frame with FIN, add to a wrapping `u64` sum the message's length, 7 when it is binary or valid UTF-8 text, and its byte at `(r * 7919 + m) % length` when it is not empty, `m` the message's index in the pass.

The UTF-8 check is RFC 3629's table of well-formed sequences, a branch per lead byte: below 0x80 one byte; 0xC2 to 0xDF one continuation; 0xE0 to 0xEF two, the first 0xA0 to 0xBF after 0xE0 and 0x80 to 0x9F after 0xED; 0xF0 to 0xF4 three, the first 0x90 to 0xBF after 0xF0 and 0x80 to 0x8F after 0xF4; anything else invalid.

Checksums (from the Go column): quick (`1 10`) `1677098`, publish (`4 5000`) `3354226142`.

What the row stands for: the scalar part of `Conn.Recv` (`ReadHalf.NextHead`, `Whole`, `Gather`, `Deliver`). The library's unmasking is `Mask` (std.simd, the `ws-mask` row in the SIMD table) and its UTF-8 check is `simd.ValidUtf8` (the `std-simd/validutf8` row); both are scalar byte loops here, so every column runs the same loop and the row compares code generators.
