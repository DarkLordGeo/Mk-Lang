// fallback, Zig: the workload of README.md with std.heap.BufferFirstAllocator
// over a 1 KiB stack buffer, then std.heap.smp_allocator, and an
// std.ArrayList(u64) per round (Zig's idiomatic pair). Its fixed-buffer
// allocator is reset each round, which makes the buffer free again. The
// sums wrap by design (+%=), as splitmix64 does.
// Usage: fallback <rounds> <seed>. Prints the checksum.
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
    const rounds = try std.fmt.parseInt(usize, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    var buf: [1024]u8 = undefined;
    var bfa: std.heap.BufferFirstAllocator = .init(&buf, std.heap.smp_allocator);
    const a = bfa.allocator();
    var total: u64 = 0;
    for (0..rounds) |_| {
        st +%= 0x9e3779b97f4a7c15;
        const r = mix(st);
        var k: usize = @intCast((r & 63) + 1);
        if (r >> 60 == 0) k = @intCast((r & 1023) + 1);
        var xs: std.ArrayList(u64) = .empty;
        for (0..k) |i| {
            try xs.append(a, r +% @as(u64, i));
        }
        var s: u64 = 0;
        for (xs.items) |x| {
            s +%= x;
        }
        total +%= s;
        xs.deinit(a);
        bfa.fixed_buffer_allocator.reset();
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
