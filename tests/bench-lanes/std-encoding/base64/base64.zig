// encoding-base64, Zig, idiomatic std: std.base64.standard. n bytes of
// splitmix64 output (seed 1), rounds times: Encoder.encode into one buffer
// and Decoder.decode into another, both allocated before the loop. The
// checksum adds, per round, the text's length, the decoded length and one
// decoded byte at a moving index.
// Usage: encoding-base64 <n> <rounds>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
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
    const text = try pa.alloc(u8, codec.Encoder.calcSize(n));
    defer pa.free(text);
    const out = try pa.alloc(u8, n);
    defer pa.free(out);
    var total: u64 = 0;
    for (0..rounds) |r| {
        const t = codec.Encoder.encode(text, data);
        const m = try codec.Decoder.calcSizeForSlice(t);
        try codec.Decoder.decode(out[0..m], t);
        total +%= t.len + m + out[(r * 7919) % n];
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
