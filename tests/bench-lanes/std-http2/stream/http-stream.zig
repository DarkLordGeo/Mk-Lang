// http-stream, Zig 0.17: n POST requests through one std.http.Client
// (keep-alive, its connection pool), each streaming a 65,536-byte file as
// its body (a File.Reader streamed into the request's BodyWriter): chunked
// for an even request, with Content-Length for an odd one. The responder
// is a concurrent task of the same program (Io.Group.concurrent) running
// the Mk column's code with no HTTP library, over the Stream.reader's
// buffer: it reads a head a byte at a time to its blank line, reads the
// body by the framing the head names (Content-Length, or chunked with no
// extensions or trailers), summing each piece into a local, and answers
// with the body's byte count times 1,000,003 plus the sum of its bytes, in
// decimal. Each reply is read whole with Reader.allocRemaining into an
// arena reset after each request. Prints the checksum: the sum over every
// reply of its status and that number. Usage: http-stream <n>.
const std = @import("std");
const Io = std.Io;
const net = std.Io.net;

const size = 65536;

// line reads one line, its LF included, into l: its length, 0 at the end
// or for a line longer than l (the Mk column's Line, a byte at a time).
fn line(r: *Io.Reader, l: *[256]u8) usize {
    var n: usize = 0;
    while (true) {
        if (r.bufferedLen() == 0) r.fillMore() catch return 0;
        const x = r.buffered()[0];
        r.toss(1);
        if (n == 256) return 0;
        l[n] = x;
        n += 1;
        if (x == 10) return n;
    }
}

// body reads n bytes, adding them to count and sum: false at the end.
fn body(r: *Io.Reader, n: usize, count: *u64, sum: *u64) bool {
    var left = n;
    while (left > 0) {
        if (r.bufferedLen() == 0) r.fillMore() catch return false;
        const b = r.buffered();
        const k = @min(b.len, left);
        var t: u64 = 0;
        for (b[0..k]) |x| t +%= x;
        sum.* +%= t;
        count.* +%= k;
        r.toss(k);
        left -= k;
    }
    return true;
}

// starts: ln begins with p, letters compared without case, as the Mk
// column's Starts (this client sends its field names in lower case).
fn starts(ln: []const u8, p: []const u8) bool {
    if (ln.len < p.len) return false;
    for (p, 0..) |c, i| {
        if ((ln[i] | 32) != (c | 32)) return false;
    }
    return true;
}

// number is the number in base (10 or 16) at the start of b.
fn number(b: []const u8, base: usize) usize {
    var v: usize = 0;
    for (b) |x| {
        const d: usize = if (x >= 48 and x <= 57) x - 48 else if (base == 16 and x >= 97 and x <= 102) x - 87 else if (base == 16 and x >= 65 and x <= 70) x - 55 else return v;
        v = v * base + d;
    }
    return v;
}

fn respond(io: Io, server: *net.Server) void {
    const s = server.accept(io) catch return;
    defer s.close(io);
    var rbuf: [16384]u8 = undefined;
    var sbuf: [256]u8 = undefined;
    var rd = s.reader(io, &rbuf);
    var w = s.writer(io, &sbuf);
    const r = &rd.interface;
    var lb: [256]u8 = undefined;
    while (true) {
        var chunked = false;
        var length: ?usize = null;
        while (true) {
            const n = line(r, &lb);
            if (n == 0) return;
            if (n == 2) break;
            if (starts(lb[0..n], "Transfer-Encoding: chunked")) {
                chunked = true;
            } else if (starts(lb[0..n], "Content-Length: ")) {
                length = number(lb[16..n], 10);
            }
        }
        var count: u64 = 0;
        var sum: u64 = 0;
        var ok = true;
        if (chunked) {
            while (ok) {
                const n = line(r, &lb);
                if (n == 0) {
                    ok = false;
                    break;
                }
                const k = number(lb[0..n], 16);
                if (k == 0) {
                    ok = line(r, &lb) == 2;
                    break;
                }
                ok = body(r, k, &count, &sum) and line(r, &lb) == 2;
            }
        } else if (length) |len| {
            if (len > 0) ok = body(r, len, &count, &sum);
        }
        if (!ok) return;
        const v = count *% 1000003 +% sum;
        var digits: [24]u8 = undefined;
        const ds = std.fmt.bufPrint(&digits, "{d}", .{v}) catch return;
        w.interface.print("HTTP/1.1 200 OK\r\nContent-Length: {d}\r\n\r\n{s}", .{ ds.len, ds }) catch return;
        w.interface.flush() catch return;
    }
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    var data: [size]u8 = undefined;
    for (&data, 0..) |*b, i| b.* = @intCast('a' + i % 26);
    const path = "http-stream-zig.tmp";
    {
        const f = try Io.Dir.cwd().createFile(io, path, .{});
        defer f.close(io);
        var fwb: [4096]u8 = undefined;
        var fw = f.writer(io, &fwb);
        try fw.interface.writeAll(&data);
        try fw.interface.flush();
    }
    defer Io.Dir.cwd().deleteFile(io, path) catch {};
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
    var total: u64 = 0;
    var ub: [64]u8 = undefined;
    var fb: [16384]u8 = undefined;
    var sb: [16384]u8 = undefined;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        const url = try std.fmt.bufPrint(&ub, "http://127.0.0.1:{d}/up/{d}", .{ port, i });
        const uri = try std.Uri.parse(url);
        const f = try Io.Dir.cwd().openFile(io, path, .{});
        defer f.close(io);
        var fr = f.reader(io, &fb);
        var req = try client.request(.POST, uri, .{});
        defer req.deinit();
        req.transfer_encoding = if (i % 2 == 1) .{ .content_length = size } else .chunked;
        var bw = try req.sendBody(&sb);
        _ = try fr.interface.streamRemaining(&bw.writer);
        try bw.end();
        var res = try req.receiveHead(&.{});
        var tb: [64]u8 = undefined;
        const b = try res.reader(&tb).allocRemaining(arena.allocator(), .unlimited);
        const v = try std.fmt.parseInt(u64, b, 10);
        total +%= @as(u64, @intFromEnum(res.head.status)) +% v;
        _ = arena.reset(.retain_capacity);
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d}\n", .{total});
    try out.interface.flush();
}
