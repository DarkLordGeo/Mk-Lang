// encoding-base64mime, Zig, idiomatic std: std.base64.standard's
// decoderWithIgnore("\r\n"). n bytes of splitmix64 output (seed 1) are
// encoded once and broken into lines of cols characters, each ending in
// CRLF (cols 0: one line, no break); rounds times the text is decoded into
// one buffer allocated before the loop. The checksum adds, per round, the
// decoded length and one decoded byte at a moving index.
// Usage: encoding-base64mime <n> <rounds> <cols>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const pa = std.heap.page_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const cols = try std.fmt.parseInt(usize, args[3], 10);
    const data = try pa.alloc(u8, n);
    defer pa.free(data);
    var st: u64 = 1;
    var i: usize = 0;
    while (i < n) {
        st +%= 0x9E3779B97F4A7C15;
        var z = st;
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
        z ^= z >> 31;
        var k: u6 = 0;
        while (k < 8 and i < n) : (k += 1) {
            data[i] = @truncate(z >> (8 * k));
            i += 1;
        }
    }
    const codec = std.base64.standard;
    const plain_buf = try pa.alloc(u8, codec.Encoder.calcSize(n));
    defer pa.free(plain_buf);
    const plain = codec.Encoder.encode(plain_buf, data);
    const text_buf = try pa.alloc(u8, plain.len + 2 * (plain.len / @max(cols, 1) + 1));
    defer pa.free(text_buf);
    var tl: usize = 0;
    if (cols == 0) {
        @memcpy(text_buf[0..plain.len], plain);
        tl = plain.len;
    } else {
        var at: usize = 0;
        while (at < plain.len) : (at += cols) {
            const end = @min(at + cols, plain.len);
            @memcpy(text_buf[tl .. tl + end - at], plain[at..end]);
            tl += end - at;
            text_buf[tl] = '\r';
            text_buf[tl + 1] = '\n';
            tl += 2;
        }
    }
    const text = text_buf[0..tl];
    const dec = codec.decoderWithIgnore("\r\n");
    const out = try pa.alloc(u8, dec.calcSizeUpperBound(text.len));
    defer pa.free(out);
    var total: u64 = 0;
    for (0..rounds) |r| {
        const m = try dec.decode(out, text);
        total +%= m + out[(r * 7919) % n];
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
