// json, Zig, idiomatic std: the task of README.md with std.json. Each round:
// std.json.parseFromSliceLeaky(std.json.Value) of the document into an
// ArenaAllocator (integers as i64, other numbers as f64), then
// std.json.Stringify.valueAlloc of the tree (compact JSON), then the parse of
// that text, and the walk of the second tree; the arena is reset after the
// round. Usage: json <file> <rounds>.
const std = @import("std");

fn walk(v: std.json.Value) i64 {
    switch (v) {
        .null => return 7,
        .bool => |b| return if (b) 3 else 5,
        .integer => |x| return 1 +% x,
        .float => |f| return 1 +% @as(i64, @intFromFloat(f)),
        .number_string => |s| return 1 +% @as(i64, @intFromFloat(std.fmt.parseFloat(f64, s) catch 0)),
        .string => |s| return 1 +% @as(i64, @intCast(s.len)),
        .array => |a| {
            var n: i64 = 1;
            for (a.items) |e| n +%= walk(e);
            return n;
        },
        .object => |o| {
            var n: i64 = 1;
            var it = o.iterator();
            while (it.next()) |kv| n +%= 1 +% @as(i64, @intCast(kv.key_ptr.len)) +% walk(kv.value_ptr.*);
            return n;
        },
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const b = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(b);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var arena = std.heap.ArenaAllocator.init(pa);
    defer arena.deinit();
    var total: i64 = 0;
    for (0..rounds) |_| {
        const a = arena.allocator();
        const v = try std.json.parseFromSliceLeaky(std.json.Value, a, b, .{});
        const s = try std.json.Stringify.valueAlloc(a, v, .{});
        const w = try std.json.parseFromSliceLeaky(std.json.Value, a, s, .{});
        total +%= walk(w);
        _ = arena.reset(.retain_capacity);
    }
    var buf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(init.io, &buf);
    try out.interface.print("{d}\n", .{total});
    try out.interface.flush();
}
