// line-echo, Zig 0.17: a buffered line echo through std.Io.Reader and
// std.Io.Writer (4096-byte buffers at each end). Each of n rounds writes
// 16 lines through the client's Writer and flushes, reads each line at
// the server with takeDelimiterInclusive('\n') and writes it back through
// the server's Writer, flushes, and reads the 16 lines back at the client
// with takeDelimiterInclusive. A line is 32 bytes: byte 0 is 32 + k mod
// 64 for the k-th line sent, then 30 'x' and a newline. sum = 31 sum +
// byte 0 + 256 * length of each line read back at the client (wrapping).
// Mode tcp runs over one loopback TCP connection (std.Io.net:
// IpAddress.listen, connect with mode stream, Server.accept; the readers
// and writers are Stream.reader and Stream.writer; TCP_NODELAY at both
// ends through the OS, since std.Io has no socket option and Zig 0.17's
// std.posix.setsockopt is a compile error); mode inmem over two in-memory
// pipes of 4096 bytes, each under its own std.Io.Mutex, one connection
// per end that holds its transport as a type-erased pointer and a vtable
// and takes a read or a write std.Io.Mutex of its own around each call
// (lock, the cancelable form, as Mk's Lock is), with a std.Io.Reader
// (stream) and a std.Io.Writer (drain) over it. The in-memory transport
// is picked at run time from the mode, as in every cell: Null for null
// (every read is the end of the stream), else End. Prints n, sum and the
// failed calls. Usage: lineecho <n> tcp|inmem|null.
const std = @import("std");
const builtin = @import("builtin");
const Io = std.Io;
const net = std.Io.net;

const lines = 16;

const Error = error{Canceled};

// Pipe holds the bytes written to one direction and not yet read.
const Pipe = struct {
    mu: Io.Mutex = .init,
    data: [4096]u8 = @splat(0),
    start: usize = 0,
    end: usize = 0,
};

// End is one end of the in-memory duplex: it reads rx and writes tx.
const End = struct {
    rx: *Pipe,
    tx: *Pipe,

    fn read(ctx: *anyopaque, io: Io, buf: []u8) Error!usize {
        const e: *End = @ptrCast(@alignCast(ctx));
        const p = e.rx;
        try p.mu.lock(io);
        defer p.mu.unlock(io);
        const n = @min(p.end - p.start, buf.len);
        @memcpy(buf[0..n], p.data[p.start .. p.start + n]);
        p.start += n;
        if (p.start == p.end) {
            p.start = 0;
            p.end = 0;
        }
        return n;
    }

    fn writeAll(ctx: *anyopaque, io: Io, data: []const u8) Error!void {
        const e: *End = @ptrCast(@alignCast(ctx));
        const p = e.tx;
        try p.mu.lock(io);
        defer p.mu.unlock(io);
        if (data.len > p.data.len - p.end) @panic("lineecho: a pipe overflowed");
        @memcpy(p.data[p.end .. p.end + data.len], data);
        p.end += data.len;
    }
};

const VTable = struct {
    read: *const fn (*anyopaque, Io, []u8) Error!usize,
    writeAll: *const fn (*anyopaque, Io, []const u8) Error!void,
};

const Conn = struct {
    reads: Io.Mutex = .init,
    writes: Io.Mutex = .init,
    ctx: *anyopaque,
    vtable: *const VTable,

    fn read(c: *Conn, io: Io, buf: []u8) Error!usize {
        try c.reads.lock(io);
        defer c.reads.unlock(io);
        return c.vtable.read(c.ctx, io, buf);
    }

    fn writeAll(c: *Conn, io: Io, data: []const u8) Error!void {
        try c.writes.lock(io);
        defer c.writes.unlock(io);
        return c.vtable.writeAll(c.ctx, io, data);
    }
};

const end_vtable: VTable = .{ .read = End.read, .writeAll = End.writeAll };

// Null reads the end of the stream and drops what is written.
const Null = struct {
    fn read(ctx: *anyopaque, io: Io, buf: []u8) Error!usize {
        _ = ctx;
        _ = io;
        _ = buf;
        return 0;
    }

    fn writeAll(ctx: *anyopaque, io: Io, data: []const u8) Error!void {
        _ = ctx;
        _ = io;
        _ = data;
    }
};

const null_vtable: VTable = .{ .read = Null.read, .writeAll = Null.writeAll };

// ConnReader is a std.Io.Reader over a Conn: each fill is one Conn.read.
const ConnReader = struct {
    conn: *Conn,
    io: Io,
    interface: Io.Reader,

    fn init(conn: *Conn, io: Io, buf: []u8) ConnReader {
        return .{ .conn = conn, .io = io, .interface = .{
            .vtable = &.{ .stream = stream },
            .buffer = buf,
            .seek = 0,
            .end = 0,
        } };
    }

    fn stream(r: *Io.Reader, w: *Io.Writer, limit: Io.Limit) Io.Reader.StreamError!usize {
        const self: *ConnReader = @alignCast(@fieldParentPtr("interface", r));
        const dest = limit.slice(try w.writableSliceGreedy(1));
        const n = self.conn.read(self.io, dest) catch return error.ReadFailed;
        if (n == 0) return error.EndOfStream;
        w.advance(n);
        return n;
    }
};

// ConnWriter is a std.Io.Writer over a Conn: each drain is one
// Conn.writeAll per non-empty piece.
const ConnWriter = struct {
    conn: *Conn,
    io: Io,
    interface: Io.Writer,

    fn init(conn: *Conn, io: Io, buf: []u8) ConnWriter {
        return .{ .conn = conn, .io = io, .interface = .{
            .vtable = &.{ .drain = drain },
            .buffer = buf,
        } };
    }

    fn put(self: *ConnWriter, b: []const u8) Io.Writer.Error!void {
        if (b.len == 0) return;
        self.conn.writeAll(self.io, b) catch return error.WriteFailed;
    }

    fn drain(w: *Io.Writer, data: []const []const u8, splat: usize) Io.Writer.Error!usize {
        const self: *ConnWriter = @alignCast(@fieldParentPtr("interface", w));
        try self.put(w.buffer[0..w.end]);
        w.end = 0;
        var n: usize = 0;
        for (data[0 .. data.len - 1]) |d| {
            try self.put(d);
            n += d.len;
        }
        const last = data[data.len - 1];
        for (0..splat) |_| {
            try self.put(last);
            n += last.len;
        }
        return n;
    }
};

const Result = struct { sum: u64, fails: u64 };

fn echo(n: u64, cw: *Io.Writer, cr: *Io.Reader, sw: *Io.Writer, sr: *Io.Reader) Result {
    var line: [32]u8 = @splat('x');
    line[31] = '\n';
    var sum: u64 = 0;
    var fails: u64 = 0;
    var k: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        for (0..lines) |_| {
            line[0] = @intCast(32 + k % 64);
            k += 1;
            cw.writeAll(&line) catch {
                fails += 1;
            };
        }
        cw.flush() catch {
            fails += 1;
        };
        for (0..lines) |_| {
            const got = sr.takeDelimiterInclusive('\n') catch blk: {
                fails += 1;
                break :blk "";
            };
            sw.writeAll(got) catch {
                fails += 1;
            };
        }
        sw.flush() catch {
            fails += 1;
        };
        for (0..lines) |_| {
            const got = cr.takeDelimiterInclusive('\n') catch "";
            if (got.len != 32) fails += 1;
            const b0: u64 = if (got.len > 0) got[0] else 0;
            sum = sum *% 31 +% b0 +% 256 * @as(u64, got.len);
        }
    }
    return .{ .sum = sum, .fails = fails };
}

extern "ws2_32" fn setsockopt(s: net.Socket.Handle, level: i32, optname: i32, optval: [*]const u8, optlen: i32) callconv(.winapi) i32;

fn noDelay(s: net.Stream) void {
    const one: c_int = 1;
    if (builtin.os.tag == .windows) {
        _ = setsockopt(s.socket.handle, 6, 1, std.mem.asBytes(&one), @sizeOf(c_int));
    } else {
        _ = std.os.linux.setsockopt(s.socket.handle, 6, 1, std.mem.asBytes(&one), @sizeOf(c_int));
    }
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const null_mode = args.len > 2 and std.mem.eql(u8, args[2], "null");
    const inmem = null_mode or (args.len > 2 and std.mem.eql(u8, args[2], "inmem"));
    var cwb: [4096]u8 = undefined;
    var crb: [4096]u8 = undefined;
    var swb: [4096]u8 = undefined;
    var srb: [4096]u8 = undefined;
    var r: Result = undefined;
    if (inmem) {
        var up: Pipe = .{};
        var down: Pipe = .{};
        var ce: End = .{ .rx = &down, .tx = &up };
        var se: End = .{ .rx = &up, .tx = &down };
        const vt: *const VTable = if (null_mode) &null_vtable else &end_vtable;
        var c: Conn = .{ .ctx = &ce, .vtable = vt };
        var s: Conn = .{ .ctx = &se, .vtable = vt };
        var cw = ConnWriter.init(&c, io, &cwb);
        var cr = ConnReader.init(&c, io, &crb);
        var sw = ConnWriter.init(&s, io, &swb);
        var sr = ConnReader.init(&s, io, &srb);
        r = echo(n, &cw.interface, &cr.interface, &sw.interface, &sr.interface);
    } else {
        const any: net.IpAddress = .{ .ip4 = net.Ip4Address.loopback(0) };
        var server = try any.listen(io, .{});
        defer server.socket.close(io);
        const d = try server.socket.address.connect(io, .{ .mode = .stream });
        defer d.close(io);
        const a = try server.accept(io);
        defer a.close(io);
        noDelay(d);
        noDelay(a);
        var cw = d.writer(io, &cwb);
        var cr = d.reader(io, &crb);
        var sw = a.writer(io, &swb);
        var sr = a.reader(io, &srb);
        r = echo(n, &cw.interface, &cr.interface, &sw.interface, &sr.interface);
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d} {d} {d}\n", .{ n, r.sum, r.fails });
    try out.interface.flush();
}
