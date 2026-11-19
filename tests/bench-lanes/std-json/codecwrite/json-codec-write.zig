// json-codec-write, Zig: std.json.parseFromSliceLeaky into a slice of
// structs of a 1 MB document once, then std.json.Stringify.valueAlloc of the
// slice (compact JSON) reps times into an ArenaAllocator reset
// (retain_capacity) after each (README.md). The checksum is reps times the
// count of the bytes { } [ ] : , and " in the last output. The struct types
// are the four levels of the Mk column, with the optional next inline as in
// Mk; R3 has no next, so its "next" (always null) is an unknown field, which
// ignore_unknown_fields lets through on the read. No libc.
const std = @import("std");

const R3 = struct {
    id: i64,
    name: []const u8,
    score: f64,
    tags: []const []const u8,
    ok: bool,
};

const R2 = struct {
    id: i64,
    name: []const u8,
    score: f64,
    tags: []const []const u8,
    ok: bool,
    next: ?R3,
};

const R1 = struct {
    id: i64,
    name: []const u8,
    score: f64,
    tags: []const []const u8,
    ok: bool,
    next: ?R2,
};

const R0 = struct {
    id: i64,
    name: []const u8,
    score: f64,
    tags: []const []const u8,
    ok: bool,
    next: ?R1,
};

// structural counts the bytes of s that are { } [ ] : , or ".
fn structural(s: []const u8) u64 {
    var n: u64 = 0;
    for (s) |c| {
        switch (c) {
            '{', '}', '[', ']', ':', ',', '"' => n += 1,
            else => {},
        }
    }
    return n;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const b = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(b);
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var tree = std.heap.ArenaAllocator.init(pa);
    defer tree.deinit();
    const rs = try std.json.parseFromSliceLeaky([]R0, tree.allocator(), b, .{ .ignore_unknown_fields = true });
    var arena = std.heap.ArenaAllocator.init(pa);
    defer arena.deinit();
    var last: u64 = 0;
    for (0..reps) |i| {
        const s = try std.json.Stringify.valueAlloc(arena.allocator(), rs, .{});
        if (i + 1 == reps) last = structural(s);
        _ = arena.reset(.retain_capacity);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{last *% @as(u64, @intCast(reps))});
    try w.interface.flush();
}
