// spawnpipe, Zig, std.process.spawn: n runs of this program with "child"
// (which copies standard input to standard output); each run gets 64 bytes
// on a piped stdin, which is then closed, its piped stdout read to the end,
// stderr ignored (the null device), and its exit waited for. Prints n, the
// bytes read back and the failed runs. Usage: spawnpipe <n>.
const std = @import("std");

const payload = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n";

fn echo(io: std.Io) !void {
    var b: [4096]u8 = undefined;
    const in = std.Io.File.stdin();
    const out = std.Io.File.stdout();
    while (true) {
        const n = in.readStreaming(io, &.{&b}) catch |e| switch (e) {
            error.EndOfStream => break,
            else => return e,
        };
        try out.writeStreamingAll(io, b[0..n]);
    }
}

fn once(io: std.Io, me: []const u8) ?usize {
    var c = std.process.spawn(io, .{
        .argv = &.{ me, "child" },
        .stdin = .pipe,
        .stdout = .pipe,
        .stderr = .ignore,
    }) catch return null;
    const w = c.stdin.?;
    c.stdin = null;
    w.writeStreamingAll(io, payload) catch {};
    w.close(io);
    const r = c.stdout.?;
    c.stdout = null;
    var b: [256]u8 = undefined;
    var k: usize = 0;
    while (k < b.len) {
        const n = r.readStreaming(io, &.{b[k..]}) catch break;
        if (n == 0) break;
        k += n;
    }
    r.close(io);
    const term = c.wait(io) catch return null;
    return if (term.success()) k else null;
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    if (std.mem.eql(u8, args[1], "child")) return echo(io);
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const me = try std.process.executablePathAlloc(io, init.arena.allocator());
    var total: usize = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        if (once(io, me)) |k| {
            if (k == 64) {
                total += 64;
                continue;
            }
        }
        fails += 1;
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &buf);
    try w.interface.print("{d} {d} {d}\n", .{ n, total, fails });
    try w.interface.flush();
}
