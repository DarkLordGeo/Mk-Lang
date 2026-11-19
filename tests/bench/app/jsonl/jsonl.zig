// jsonl, Zig, std: the task of README.md with std.json. The file is read
// through std.Io.File's Reader over a 64 KiB buffer, one line at a time with
// takeDelimiter; std.json.parseFromSliceLeaky(Event) decodes each line into
// an Event over a line ArenaAllocator that is reset after every line
// (strings with no escape are views of the line, as the default allows).
// Kept events are grouped by user in a std.StringHashMap(Group) (its default
// hash, std.hash.Wyhash with seed 0), each new key copied into the
// program's arena; the groups are sorted with std.sort.pdq, and the Report
// is written by std.json.Stringify.value (compact JSON, members in field
// order) to a buffered stdout writer, then a line feed.
// Usage: jsonl <file> [<lines>].
const std = @import("std");

const Event = struct {
    id: i64,
    user: []const u8,
    event: []const u8,
    status: i64,
    bytes: i64,
    ms: i64,
    delta: i64,
    ok: bool,
    tags: []const []const u8,
    msg: []const u8,
};

const Group = struct {
    user: []const u8,
    count: i64,
    first: i64,
    bytes: i64,
    ms_min: i64,
    ms_max: i64,
    delta: i64,
    tags: i64,
    msg: i64,
    buys: i64,
    fails: i64,

    fn add(g: *Group, e: *const Event) void {
        g.count += 1;
        g.bytes += e.bytes;
        g.ms_min = @min(g.ms_min, e.ms);
        g.ms_max = @max(g.ms_max, e.ms);
        g.delta += e.delta;
        g.tags += @intCast(e.tags.len);
        g.msg += @intCast(e.msg.len);
        if (std.mem.eql(u8, e.event, "buy")) g.buys += 1;
        if (e.status >= 400) g.fails += 1;
    }
};

const Report = struct {
    lines: i64,
    kept: i64,
    dropped: i64,
    groups: []const Group,
};

fn before(_: void, a: Group, b: Group) bool {
    if (a.count != b.count) return a.count > b.count;
    if (a.bytes != b.bytes) return a.bytes > b.bytes;
    return std.mem.order(u8, a.user, b.user) == .lt;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len < 2 or args.len > 3) return error.Usage;
    const limit: i64 = if (args.len == 3) try std.fmt.parseInt(i64, args[2], 10) else -1;
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    const a = arena.allocator();
    var line_arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer line_arena.deinit();
    const file = try std.Io.Dir.cwd().openFile(init.io, args[1], .{});
    defer file.close(init.io);
    const rbuf = try a.alloc(u8, 64 << 10);
    var fr = file.reader(init.io, rbuf);
    const r = &fr.interface;
    var groups = std.StringHashMap(Group).init(a);
    var n: i64 = 0;
    var kept: i64 = 0;
    while (n != limit) {
        const line = try r.takeDelimiter('\n') orelse break;
        n += 1;
        defer _ = line_arena.reset(.retain_capacity);
        const e = try std.json.parseFromSliceLeaky(Event, line_arena.allocator(), line, .{});
        if (!e.ok) continue;
        kept += 1;
        const slot = try groups.getOrPut(e.user);
        if (!slot.found_existing) {
            const user = try a.dupe(u8, e.user);
            slot.key_ptr.* = user;
            slot.value_ptr.* = .{ .user = user, .count = 0, .first = e.id, .bytes = 0, .ms_min = e.ms, .ms_max = e.ms, .delta = 0, .tags = 0, .msg = 0, .buys = 0, .fails = 0 };
        }
        slot.value_ptr.add(&e);
    }
    const gs = try a.alloc(Group, groups.count());
    var it = groups.valueIterator();
    var i: usize = 0;
    while (it.next()) |g| : (i += 1) gs[i] = g.*;
    std.sort.pdq(Group, gs, {}, before);
    const wbuf = try a.alloc(u8, 64 << 10);
    var w = std.Io.File.stdout().writer(init.io, wbuf);
    try std.json.Stringify.value(Report{ .lines = n, .kept = kept, .dropped = n - kept, .groups = gs }, .{}, &w.interface);
    try w.interface.writeByte('\n');
    try w.interface.flush();
}
