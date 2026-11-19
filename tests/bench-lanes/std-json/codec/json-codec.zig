// json-codec, Zig: std.json.parseFromSliceLeaky into a slice of structs of a
// 1 MB document, reps times into one ArenaAllocator reset (retain_capacity)
// after each, and a sum over the records (README.md). The struct types are
// the four levels of the Mk column, with the optional next inline as in Mk;
// R3 has no next, so its "next" (always null) is an unknown field, which
// ignore_unknown_fields lets through as Mk and Go do. No libc.
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

// recSum is one record's share of the checksum: 1, its id, its name's
// length, its score truncated, its tags' count and lengths, 1 if ok.
fn recSum(r: anytype) i64 {
    var n: i64 = 1 +% r.id +% @as(i64, @intCast(r.name.len)) +% @as(i64, @intFromFloat(r.score));
    n +%= @intCast(r.tags.len);
    for (r.tags) |t| n +%= @intCast(t.len);
    if (r.ok) n +%= 1;
    return n;
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
    var total: i64 = 0;
    for (0..reps) |_| {
        const rs = try std.json.parseFromSliceLeaky([]R0, arena.allocator(), b, .{ .ignore_unknown_fields = true });
        for (rs) |*r0| {
            total +%= recSum(r0);
            if (r0.next) |*r1| {
                total +%= recSum(r1);
                if (r1.next) |*r2| {
                    total +%= recSum(r2);
                    if (r2.next) |*r3| total +%= recSum(r3);
                }
            }
        }
        _ = arena.reset(.retain_capacity);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
