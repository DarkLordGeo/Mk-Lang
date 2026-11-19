// crypto-equal, Zig: std.crypto.timing_safe.compare(u8, a, b, .little) ==
// .eq over the input of mk/crypto-equal.mk's comment (CRY-9). Zig's std has
// no constant-time equality over runtime-length slices: timing_safe.eql
// takes a comptime array or vector type, and compare, the runtime-length
// form, computes an ordering (greater, equal or less, scanning from the
// last byte), which is more work than equality. It asserts equal lengths;
// these inputs always are.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const mib = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const n = mib * 1048576;
    const a = try std.heap.page_allocator.alloc(u8, n);
    defer std.heap.page_allocator.free(a);
    const b = try std.heap.page_allocator.alloc(u8, n);
    defer std.heap.page_allocator.free(b);
    var i: usize = 0;
    while (i < n) : (i += 8) {
        st +%= 0x9e3779b97f4a7c15;
        std.mem.writeInt(u64, a[i..][0..8], mix(st), .little);
    }
    @memcpy(b, a);
    var count: u64 = 0;
    for (0..reps) |r| {
        const j = (r * 7919) % n;
        if (r % 2 == 1) b[j] ^= 1;
        if (std.crypto.timing_safe.compare(u8, a, b, .little) == .eq) count += 1;
        if (r % 2 == 1) b[j] ^= 1;
    }
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{(count *% 1000003) ^ @as(u64, reps)});
    try w.interface.flush();
}
