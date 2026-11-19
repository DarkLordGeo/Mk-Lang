// fixed-alloc, Zig: the workload of README.md with
// std.heap.FixedBufferAllocator over a 64 KiB stack buffer: two create(u64)
// per iteration, destroyed in LIFO order, through the Allocator interface.
// The sums wrap by design (+%=), as splitmix64 does.
// Usage: fixedalloc <n> <seed>. Prints the checksum.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    var buf: [65536]u8 = undefined;
    var fba = std.heap.FixedBufferAllocator.init(&buf);
    const a = fba.allocator();
    var total: u64 = 0;
    for (0..n) |_| {
        st +%= 0x9e3779b97f4a7c15;
        const b1 = try a.create(u64);
        b1.* = mix(st);
        const b2 = try a.create(u64);
        b2.* = st;
        total +%= b2.*;
        a.destroy(b2);
        total ^= b1.*;
        a.destroy(b1);
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
