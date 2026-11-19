// http-fetch, Zig 0.17: n GET requests through one std.http.Client
// (keep-alive, its connection pool) to a responder in a concurrent task of
// the same program (Io.Group.concurrent), over loopback. The responder is
// the Mk column's code and uses no HTTP library: it reads a request's head
// to its blank line (the same scalar scan, over the Stream.reader's
// buffer), takes the id from the target and writes a fixed head with a
// body of 16 + id % 1024 letters. The targets come from the same
// splitmix64 stream (seed 1). Each body is read whole with
// Reader.allocRemaining into an arena reset after each request. Prints the
// checksum: the sum over every reply of its status, its body's length and
// its first and last bytes. Usage: http-fetch <n>.
const std = @import("std");
const Io = std.Io;
const net = std.Io.net;

const Rng = struct {
    s: u64,
    fn next(g: *Rng) u64 {
        g.s +%= 0x9E3779B97F4A7C15;
        var z = g.s;
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
        return z ^ (z >> 31);
    }
};

const letters = "abcdefghijklmnopqrstuvwxyz";

// headEnd is the offset just past the first CRLF CRLF in b, null when b
// holds none: the Mk column's HeadEnd.
fn headEnd(b: []const u8) ?usize {
    if (b.len < 4) return null;
    var i: usize = 0;
    while (i < b.len - 3) : (i += 1) {
        if (b[i] == 13 and b[i + 1] == 10 and b[i + 2] == 13 and b[i + 3] == 10) return i + 4;
    }
    return null;
}

// id is the decimal number after "/items/" in the request line of h: the
// Mk column's Id.
fn id(h: []const u8) usize {
    var i: usize = 0;
    while (i < h.len and h[i] != '/') i += 1;
    i += 7;
    var v: usize = 0;
    while (i < h.len and h[i] >= '0' and h[i] <= '9') : (i += 1) v = v * 10 + (h[i] - '0');
    return v;
}

fn respond(io: Io, server: *net.Server) void {
    const s = server.accept(io) catch return;
    defer s.close(io);
    var rbuf: [16384]u8 = undefined;
    var sbuf: [2048]u8 = undefined;
    var r = s.reader(io, &rbuf);
    var w = s.writer(io, &sbuf);
    var wb: [2048]u8 = undefined;
    while (true) {
        var end = headEnd(r.interface.buffered());
        while (end == null) {
            r.interface.fillMore() catch return;
            end = headEnd(r.interface.buffered());
        }
        const e = end.?;
        const size = 16 + id(r.interface.buffered()[0..e]) % 1024;
        const head = std.fmt.bufPrint(&wb, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: {d}\r\n\r\n", .{size}) catch return;
        var n = head.len;
        var k: usize = 0;
        while (k < size) : (k += 1) {
            wb[n] = letters[k % 26];
            n += 1;
        }
        w.interface.writeAll(wb[0..n]) catch return;
        w.interface.flush() catch return;
        r.interface.toss(e);
    }
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const any: net.IpAddress = .{ .ip4 = net.Ip4Address.loopback(0) };
    var server = try any.listen(io, .{});
    defer server.socket.close(io);
    const port = server.socket.address.getPort();
    var group: Io.Group = .init;
    defer group.cancel(io);
    try group.concurrent(io, respond, .{ io, &server });
    var client: std.http.Client = .{ .allocator = init.gpa, .io = io };
    defer client.deinit();
    var arena = std.heap.ArenaAllocator.init(init.gpa);
    defer arena.deinit();
    var g: Rng = .{ .s = 1 };
    var total: u64 = 0;
    var ub: [64]u8 = undefined;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        const url = try std.fmt.bufPrint(&ub, "http://127.0.0.1:{d}/items/{d}", .{ port, g.next() % 1000000 });
        const uri = try std.Uri.parse(url);
        var req = try client.request(.GET, uri, .{});
        defer req.deinit();
        try req.sendBodiless();
        var res = try req.receiveHead(&.{});
        var tb: [64]u8 = undefined;
        const b = try res.reader(&tb).allocRemaining(arena.allocator(), .unlimited);
        total +%= @as(u64, @intFromEnum(res.head.status)) +% b.len;
        if (b.len > 0) total +%= @as(u64, b[0]) +% b[b.len - 1];
        _ = arena.reset(.retain_capacity);
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d}\n", .{total});
    try out.interface.flush();
}
