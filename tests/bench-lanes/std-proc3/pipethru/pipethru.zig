// pipethru, Zig, std.process.spawn: this program runs itself with
// "child n", which writes n bytes of a splitmix64 stream (each word's 8
// bytes, little endian, from state 0) to its standard output in 64 KiB
// writes; the parent reads the child's piped stdout with readStreaming
// into a 64 KiB buffer and prints the bytes read, their sum and the
// child's exit code. stdin and stderr are ignored (the null device).
// Usage: pipethru <n>.
const std = @import("std");

const chunk = 65536;

fn fill(b: []u8, x0: u64) u64 {
    var x = x0;
    var i: usize = 0;
    while (i + 8 <= b.len) : (i += 8) {
        x +%= 0x9e3779b97f4a7c15;
        var z = x;
        z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
        z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
        z ^= z >> 31;
        std.mem.writeInt(u64, b[i..][0..8], z, .little);
    }
    return x;
}

fn child(io: std.Io, n: usize) !void {
    var b: [chunk]u8 = undefined;
    const out = std.Io.File.stdout();
    _ = fill(&b, 0);
    var left = n;
    while (left > 0) {
        const k = @min(left, chunk);
        try out.writeStreamingAll(io, b[0..k]);
        left -= k;
    }
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len < 2) return error.Usage;
    if (std.mem.eql(u8, args[1], "child")) {
        if (args.len != 3) return error.Usage;
        return child(io, try std.fmt.parseInt(usize, args[2], 10));
    }
    const me = try std.process.executablePathAlloc(io, init.arena.allocator());
    var c = try std.process.spawn(io, .{
        .argv = &.{ me, "child", args[1] },
        .stdin = .ignore,
        .stdout = .pipe,
        .stderr = .ignore,
    });
    const r = c.stdout.?;
    c.stdout = null;
    var b: [chunk]u8 = undefined;
    var total: u64 = 0;
    var sum: u64 = 0;
    while (true) {
        const k = r.readStreaming(io, &.{&b}) catch |e| switch (e) {
            error.EndOfStream => break,
            else => return e,
        };
        if (k == 0) break;
        total += k;
        for (b[0..k]) |v| sum +%= v;
    }
    r.close(io);
    const term = try c.wait(io);
    const code: i64 = if (term.success()) 0 else -1;
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &buf);
    try w.interface.print("{d} {d} {d}\n", .{ total, sum, code });
    try w.interface.flush();
}
