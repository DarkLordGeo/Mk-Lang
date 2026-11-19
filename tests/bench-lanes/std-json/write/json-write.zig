// json-write, Zig: std.json.parseFromSliceLeaky into std.json.Value of a 1 MB
// document once, with parse_numbers = false so numbers stay text as in Mk,
// then std.json.Stringify.valueAlloc of the tree (compact JSON) reps times
// into an ArenaAllocator reset (retain_capacity) after each (README.md).
// No libc.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const b = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(b);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var tree = std.heap.ArenaAllocator.init(pa);
    defer tree.deinit();
    const v = try std.json.parseFromSliceLeaky(std.json.Value, tree.allocator(), b, .{ .parse_numbers = false });
    var arena = std.heap.ArenaAllocator.init(pa);
    defer arena.deinit();
    var total: u64 = 0;
    for (0..reps) |_| {
        const s = try std.json.Stringify.valueAlloc(arena.allocator(), v, .{});
        total +%= s.len;
        _ = arena.reset(.retain_capacity);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
