// udp-pingpong, Zig 0.17, std.Io.net: two UDP sockets (IpAddress.bind with
// mode dgram) on 127.0.0.1; each of n rounds sends 32 bytes (byte 0 the
// round's low byte) from a to b with Socket.send, receives them on b with
// Socket.receive, sends them back to the source and receives them on a.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. Usage: udppingpong <n>.
const std = @import("std");
const net = std.Io.net;

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const any: net.IpAddress = .{ .ip4 = net.Ip4Address.loopback(0) };
    const a = try any.bind(io, .{ .mode = .dgram });
    defer a.close(io);
    const b = try any.bind(io, .{ .mode = .dgram });
    defer b.close(io);
    var msg: [32]u8 = @splat(7);
    var buf: [64]u8 = undefined;
    var sum: u64 = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        msg[0] = @truncate(i);
        var ok = true;
        a.send(io, &b.address, &msg) catch {
            ok = false;
        };
        var from = b.address;
        if (b.receive(io, &buf)) |m| {
            if (m.data.len != 32) ok = false;
            from = m.from;
        } else |_| {
            ok = false;
        }
        b.send(io, &from, buf[0..32]) catch {
            ok = false;
        };
        if (a.receive(io, &buf)) |m| {
            if (m.data.len != 32) ok = false;
        } else |_| {
            ok = false;
        }
        if (!ok) fails += 1;
        sum = sum *% 31 +% buf[0];
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d} {d} {d}\n", .{ n, sum, fails });
    try out.interface.flush();
}
