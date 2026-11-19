// inmem-conn, Zig 0.17: a connection over an in-memory transport, as a
// protocol layer sees one. The transport keeps one 32-byte message under
// its own std.Io.Mutex; the connection holds it as a type-erased pointer
// and a vtable (the shape of std.Io.Reader and std.Io.Writer) and takes a
// read or a write std.Io.Mutex of its own around each call (lock, the
// cancelable form, as Mk's Lock is). Each of n rounds writes 32 bytes
// (byte 0 the round's low byte) and reads them back. sum = 31 sum + byte
// 0 of each read (wrapping). The transport is picked at run time from the
// mode, as in every cell: Null for null (every read is the end of the
// stream), else Loop; Mk's direct mode is Loop here. Prints n, sum and the
// failed calls. Usage: inmemconn <n> [mode].
const std = @import("std");
const Io = std.Io;

const Error = error{Canceled};

const Loop = struct {
    mu: Io.Mutex = .init,
    data: [32]u8 = @splat(0),
    n: usize = 0,

    fn read(ctx: *anyopaque, io: Io, buf: []u8) Error!usize {
        const l: *Loop = @ptrCast(@alignCast(ctx));
        try l.mu.lock(io);
        defer l.mu.unlock(io);
        const n = @min(l.n, buf.len);
        @memcpy(buf[0..n], l.data[0..n]);
        l.n = 0;
        return n;
    }

    fn writeAll(ctx: *anyopaque, io: Io, data: []const u8) Error!void {
        const l: *Loop = @ptrCast(@alignCast(ctx));
        try l.mu.lock(io);
        defer l.mu.unlock(io);
        const n = @min(data.len, 32);
        @memcpy(l.data[0..n], data[0..n]);
        l.n = n;
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

const loop_vtable: VTable = .{ .read = Loop.read, .writeAll = Loop.writeAll };

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

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(u64, args[1], 10);
    var l: Loop = .{};
    const null_mode = args.len > 2 and std.mem.eql(u8, args[2], "null");
    const vt: *const VTable = if (null_mode) &null_vtable else &loop_vtable;
    var c: Conn = .{ .ctx = &l, .vtable = vt };
    var msg: [32]u8 = @splat(7);
    var buf: [32]u8 = @splat(0);
    var sum: u64 = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        msg[0] = @truncate(i);
        var ok = true;
        c.writeAll(io, &msg) catch {
            ok = false;
        };
        const k = c.read(io, &buf) catch 0;
        if (!ok or k != 32) fails += 1;
        sum = sum *% 31 +% buf[0];
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d} {d} {d}\n", .{ n, sum, fails });
    try out.interface.flush();
}
