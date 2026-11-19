// url-parse, Zig, idiomatic std: std.Uri.parse. n URLs made from
// splitmix64 output (seed 1) by the generator README.md states (one in
// eight with an upper-case scheme and first host label, one in eight with
// an IPv4 host), then rounds times: parse each. The checksum adds, per URL and round, the lengths of
// the scheme, host, path, query and fragment, and the port.
// Usage: url-parse <n> <rounds>.
const std = @import("std");

const alnum = "abcdefghijklmnopqrstuvwxyz0123456789";
const upper_alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
const path_chars = "abcdefghijklmnopqrstuvwxyz0123456789-_~";

const Gen = struct {
    st: u64,
    fn next(g: *Gen) u64 {
        g.st +%= 0x9E3779B97F4A7C15;
        var z = g.st;
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
        return z ^ (z >> 31);
    }
    fn pick(g: *Gen, n: usize) usize {
        return @intCast(g.next() % n);
    }
    fn word(g: *Gen, list: *std.ArrayList(u8), a: std.mem.Allocator, chars: []const u8, count: usize) !void {
        for (0..count) |_| {
            try list.append(a, chars[g.pick(chars.len)]);
        }
    }
};

fn makeUrl(g: *Gen, a: std.mem.Allocator) ![]u8 {
    const z = g.next();
    const schemes = [_][]const u8{ "http", "https", "ws", "wss", "ftp" };
    const upper_schemes = [_][]const u8{ "HTTP", "HTTPS", "WS", "WSS", "FTP" };
    const ports = [_]u16{ 0, 8080, 8443, 3000 };
    const tlds = [_][]const u8{ "com", "org", "net", "dev" };
    var b: std.ArrayList(u8) = .empty;
    const upper = (z >> 44) % 8 == 0;
    try b.appendSlice(a, if (upper) upper_schemes[@intCast(z % 5)] else schemes[@intCast(z % 5)]);
    try b.appendSlice(a, "://");
    if ((z >> 47) % 8 == 0) {
        const x = g.pick(256);
        const y = g.pick(256);
        const w = g.pick(256);
        var hbuf: [16]u8 = undefined;
        try b.appendSlice(a, try std.fmt.bufPrint(&hbuf, "10.{d}.{d}.{d}", .{ x, y, w }));
    } else {
        const labels = 1 + @as(usize, @intCast((z >> 16) % 3));
        for (0..labels) |k| {
            try g.word(&b, a, if (upper and k == 0) upper_alnum else alnum, 3 + g.pick(8));
            try b.append(a, '.');
        }
        try b.appendSlice(a, tlds[@intCast((z >> 24) % 4)]);
    }
    const port = ports[@intCast((z >> 8) % 4)];
    if (port != 0) {
        var buf: [8]u8 = undefined;
        try b.appendSlice(a, try std.fmt.bufPrint(&buf, ":{d}", .{port}));
    }
    try b.append(a, '/');
    const segments: usize = @intCast((z >> 32) % 6);
    for (0..segments) |s| {
        if (s > 0) try b.append(a, '/');
        try g.word(&b, a, path_chars, 1 + g.pick(12));
    }
    if (segments > 0 and (z >> 40) & 1 == 1) try b.append(a, '/');
    if ((z >> 41) & 1 == 1) {
        try b.append(a, '?');
        const pairs = 1 + g.pick(3);
        for (0..pairs) |p| {
            if (p > 0) try b.append(a, '&');
            try g.word(&b, a, alnum, 1 + g.pick(6));
            try b.append(a, '=');
            try g.word(&b, a, alnum, g.pick(8));
        }
    }
    if ((z >> 42) & 3 == 0) {
        try b.append(a, '#');
        try g.word(&b, a, alnum, 1 + g.pick(10));
    }
    return b.toOwnedSlice(a);
}

fn len(c: ?std.Uri.Component) usize {
    const v = c orelse return 0;
    return switch (v) {
        .raw => |s| s.len,
        .percent_encoded => |s| s.len,
    };
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const a = init.arena.allocator();
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var g = Gen{ .st = 1 };
    const urls = try a.alloc([]u8, n);
    for (urls) |*u| u.* = try makeUrl(&g, a);
    var total: u64 = 0;
    for (0..rounds) |_| {
        for (urls) |text| {
            const u = try std.Uri.parse(text);
            const port: u64 = u.port orelse 0;
            total +%= u.scheme.len + len(u.host) + len(u.path) + len(u.query) + len(u.fragment) + port;
        }
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
