// ws-mask, Zig: RFC 6455 5.3's masking as std.websocket's Mask does it, a
// scalar head to phase 0, 16 bytes a step as a @Vector(16, u8) XOR on
// unaligned loads and stores, a scalar tail. n bytes of splitmix64 output
// (seed 1) in a buffer that starts on a 64-byte boundary (BS-4), rounds
// times masked in place with round r's key (the four low
// bytes of (r + 1) * 0x9E3779B97F4A7C15) from phase r % 4; the checksum
// adds, per round, the byte at (r * 7919) % n and the returned phase.
// Usage: ws-mask <n> <rounds>.
const std = @import("std");

fn mask(b: []u8, key: [4]u8, phase: usize) usize {
    const n = b.len;
    var i: usize = 0;
    var p = phase & 3;
    while (i < n and p != 0) : (i += 1) {
        b[i] ^= key[p];
        p = (p + 1) & 3;
    }
    const k: @Vector(16, u8) = .{ key[0], key[1], key[2], key[3], key[0], key[1], key[2], key[3], key[0], key[1], key[2], key[3], key[0], key[1], key[2], key[3] };
    while (n - i >= 16) : (i += 16) {
        const v: @Vector(16, u8) = b[i..][0..16].*;
        b[i..][0..16].* = v ^ k;
    }
    while (i < n) : (i += 1) {
        b[i] ^= key[p];
        p = (p + 1) & 3;
    }
    return p;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    const data = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(data);
    if (@intFromPtr(data.ptr) % 64 != 0) {
        std.debug.print("ws-mask: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
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
    var total: u64 = 0;
    for (0..rounds) |r| {
        const kw: u64 = (@as(u64, r) +% 1) *% 0x9E3779B97F4A7C15;
        const key = [4]u8{ @truncate(kw), @truncate(kw >> 8), @truncate(kw >> 16), @truncate(kw >> 24) };
        const p = mask(data, key, r % 4);
        total +%= @as(u64, data[(r * 7919) % n]) + p;
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
