# gzip (idiomatic)

**Task.** Inflate 100 MB with the standard library's gzip decompressor, where one exists: Mk (`std.compress.Inflater` over `fs.File.Reader`), Go and Zig. `gzip <file> <rounds>`: the compressed file is read once; each round decompresses it from memory and reads the output in 64 KiB chunks; the checksum adds every decompressed byte and the number of bytes. Every column checks the gzip trailer's CRC-32 and length and fails on a mismatch: Go's `gzip.Reader` and Mk's `Inflater` do it inside the decompressor; Zig 0.17's `flate.Decompress` reads the trailer but never compares it (`Decompress.zig`, case `.protocol_footer`, only stores `crc` and `count`), so the Zig program computes both itself with std's gzip hasher, `flate.Container.Hasher` (`std.hash.Crc32` and the length, the hasher `flate.Compress` writes the trailer with), over every chunk, and compares them with the trailer Decompress read. Without that, the Zig cells would skip a CRC-32 pass over all 100 MB a round that the other columns pay.

**Input.** `tools/bench gen` kind `gzip`, seed 6: the `words` text of `wordfreq` (100,000,000 bytes before compression) compressed once by Go's `compress/gzip` at the default level. The checksum is over the decompressed bytes, so another Go release's compressor gives the same checksum.

**Cells.**

| Column | Decompressor |
|---|---|
| mk | `std.compress.Inflater.NewGzip` over `Reader.Of(gz)`, `gz` the file's bytes read once with `fs.ReadBytes`; `Read` into a 64 KiB buffer, the Inflater's window and input buffer from one arena reset each round. The bytes stay in memory, so no round reads the file again through `fs.File.Reader`. The one-shot form (`compress.Gunzip` into one arena) is the row `std-compress/compress-gunzip` under `tests/bench-lanes`, not this task |
| zig-fast, zig-safe | `std.compress.flate.Decompress` with the `.gzip` container over a fixed `std.Io.Reader`, `readSliceShort`; the trailer checked by the program with `flate.Container.Hasher` |
| go | `compress/gzip.NewReader` over a `bytes.Reader`, `Read` |
| c | no std gzip in C (zlib is not std) |
| rust | no std gzip in Rust (`flate2` is a crate; no crate stands in) |

Sizes: quick `{data} 1` (9987345428), publish `{data} 4` (four times that; the fastest cell ran one round in 0.50 s in a verify run on a shared machine, 2026-11-07, without the Zig cells' CRC-32 pass, which the 2026-11-07 table's Zig cells also lack). Overflow class `wraps`.
