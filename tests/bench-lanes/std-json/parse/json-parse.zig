// json-parse, Zig: std.json.parseFromSliceLeaky into std.json.Value of a 1 MB
// document, reps times into one ArenaAllocator reset (retain_capacity) after
// each parse, and a walk of each tree (README.md). parse_numbers = false
// keeps numbers as text (.number_string), as Mk and Go keep them (JSON-8).
// No libc.
const std = @import("std");

fn walk(v: std.json.Value) u64 {
    switch (v) {
        .string => |s| return 1 + s.len,
        .array => |a| {
            var n: u64 = 1;
            for (a.items) |e| n += walk(e);
            return n;
        },
        .object => |o| {
            var n: u64 = 1;
            var it = o.iterator();
            while (it.next()) |kv| n += kv.key_ptr.len + walk(kv.value_ptr.*);
            return n;
        },
        else => return 1,
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const b = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(b);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var arena = std.heap.ArenaAllocator.init(pa);
    defer arena.deinit();
    var total: u64 = 0;
    for (0..reps) |_| {
        const v = try std.json.parseFromSliceLeaky(std.json.Value, arena.allocator(), b, .{ .parse_numbers = false });
        total +%= walk(v);
        _ = arena.reset(.retain_capacity);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
