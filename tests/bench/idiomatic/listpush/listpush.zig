// listpush, Zig, idiomatic std: the task of README.md with
// std.ArrayList(u64) over std.heap.smp_allocator: per round a list from
// .empty (no capacity), n single appends, one appendSlice of n/4, a read of
// every element, then deinit. Usage: listpush <n> <rounds> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn round(gpa: std.mem.Allocator, n: usize, ext: []const u64, st: *u64) !u64 {
    var l: std.ArrayList(u64) = .empty;
    defer l.deinit(gpa);
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st.* +%= 0x9e3779b97f4a7c15;
        try l.append(gpa, mix(st.*));
    }
    try l.appendSlice(gpa, ext);
    var sum: u64 = 0;
    for (l.items) |x| sum +%= x;
    return sum +% l.items.len;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const gpa = std.heap.smp_allocator;
    const ext = try gpa.alloc(u64, n / 4);
    defer gpa.free(ext);
    for (ext, 0..) |*x, i| x.* = 3 * @as(u64, i) + 1;
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(gpa, n, ext, &st);
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
