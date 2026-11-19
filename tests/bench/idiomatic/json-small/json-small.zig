// json-small, Zig, idiomatic std: the task of README.md with std.json on
// small documents: each line of the file (std.mem.splitScalar at '\n'; the
// array's "[", "," or "]" cut off; an empty line skipped) is one document.
// Per document: std.json.parseFromSliceLeaky(std.json.Value) into an
// ArenaAllocator over std.heap.page_allocator, std.json.Stringify.valueAlloc
// (compact JSON), parseFromSliceLeaky of that text, the walk of the second
// tree, then reset(.retain_capacity). Usage: json-small <file> <rounds>.
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

fn doc(line: []const u8) []const u8 {
    var d = line;
    if (std.mem.startsWith(u8, d, "[")) d = d[1..];
    if (std.mem.endsWith(u8, d, ",") or std.mem.endsWith(u8, d, "]")) d = d[0 .. d.len - 1];
    return d;
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
        var lines = std.mem.splitScalar(u8, b, '\n');
        while (lines.next()) |line| {
            const d = doc(line);
            if (d.len == 0) continue;
            const a = arena.allocator();
            const v = try std.json.parseFromSliceLeaky(std.json.Value, a, d, .{});
            const s = try std.json.Stringify.valueAlloc(a, v, .{});
            const w = try std.json.parseFromSliceLeaky(std.json.Value, a, s, .{});
            total +%= walk(w);
            _ = arena.reset(.retain_capacity);
        }
    }
    var buf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(init.io, &buf);
    try out.interface.print("{d}\n", .{total});
    try out.interface.flush();
}
