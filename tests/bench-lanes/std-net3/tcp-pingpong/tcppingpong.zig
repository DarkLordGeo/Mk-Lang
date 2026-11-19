// tcp-pingpong, Zig 0.17, std.Io.net: one loopback TCP connection
// (IpAddress.listen, IpAddress.connect with mode stream, Server.accept) of
// one thread, both ends with TCP_NODELAY; each of n rounds writes 32 bytes
// (byte 0 the round's low byte) at the dialled end through its
// Stream.writer (writeAll, flush), reads them whole at the accepted end
// through its Stream.reader (readSliceAll), writes them back and reads
// them whole at the dialled end. sum = 31 sum + byte 0 of each answer
// (wrapping). Prints n, sum and the failed calls. The mode argument is
// Mk's and ignored. Usage: tcppingpong <n> [mode].
const std = @import("std");
const builtin = @import("builtin");
const net = std.Io.net;

// Zig 0.17's std.posix.setsockopt is a compile error ("use std.Io
// instead") and std.Io has no socket option, so the cell calls the OS.
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
    const any: net.IpAddress = .{ .ip4 = net.Ip4Address.loopback(0) };
    var server = try any.listen(io, .{});
    defer server.socket.close(io);
    const d = try server.socket.address.connect(io, .{ .mode = .stream });
    defer d.close(io);
    const s = try server.accept(io);
    defer s.close(io);
    noDelay(d);
    noDelay(s);
    var drb: [64]u8 = undefined;
    var dwb: [64]u8 = undefined;
    var srb: [64]u8 = undefined;
    var swb: [64]u8 = undefined;
    var dr = d.reader(io, &drb);
    var dw = d.writer(io, &dwb);
    var sr = s.reader(io, &srb);
    var sw = s.writer(io, &swb);
    var msg: [32]u8 = @splat(7);
    var buf: [32]u8 = undefined;
    var sum: u64 = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        msg[0] = @truncate(i);
        var ok = true;
        dw.interface.writeAll(&msg) catch {
            ok = false;
        };
        dw.interface.flush() catch {
            ok = false;
        };
        sr.interface.readSliceAll(&buf) catch {
            ok = false;
        };
        sw.interface.writeAll(&buf) catch {
            ok = false;
        };
        sw.interface.flush() catch {
            ok = false;
        };
        dr.interface.readSliceAll(&buf) catch {
            ok = false;
        };
        if (!ok) fails += 1;
        sum = sum *% 31 +% buf[0];
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d} {d} {d}\n", .{ n, sum, fails });
    try out.interface.flush();
}
