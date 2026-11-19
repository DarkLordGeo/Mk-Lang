// ws-extend, Zig: std.ArrayList(u8).appendSlice on std.heap.smp_allocator.
// n bytes of splitmix64 output (seed 1); per round r a new, empty growable
// byte list gets the n bytes appended in order, piece j of 1 + (r + j) % piece
// bytes (the last cut at n), then is freed; the checksum adds, per round,
// the byte at (r * 7919) % n and the number of pieces. Usage: ws-extend <n>
// <piece> <rounds>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const pa = std.heap.page_allocator;
    const gpa = std.heap.smp_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const piece = try std.fmt.parseInt(usize, args[2], 10);
    const rounds = try std.fmt.parseInt(usize, args[3], 10);
    const data = try pa.alloc(u8, n);
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
        var v: std.ArrayList(u8) = .empty;
        var at: usize = 0;
        var j: usize = 0;
        while (at < n) {
            const k = @min(1 + (r + j) % piece, n - at);
            try v.appendSlice(gpa, data[at .. at + k]);
            at += k;
            j += 1;
        }
        total +%= @as(u64, v.items[(r * 7919) % n]) +% @as(u64, j);
        v.deinit(gpa);
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
