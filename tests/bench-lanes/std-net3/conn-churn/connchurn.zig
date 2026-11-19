// conn-churn, Zig 0.17, std.Io.net: n loopback TCP connections, one after
// another, of one thread. Each round connects to the listener
// (IpAddress.connect with mode stream), accepts the connection
// (Server.accept, whose Stream.socket.address is the peer's address from
// the accept itself), writes two bytes (the round's low byte twice) at the
// accepted end through its Stream.writer (a 2-byte buffer, so one send),
// reads one at the connected end through its Stream.reader (takeByte over a
// 1-byte buffer, so one byte leaves the socket), and closes the connected
// end, then the accepted one. The connected end closes with a byte unread,
// so its TCP resets the connection (RFC 1122 4.2.2.13) and no TIME_WAIT
// entry is left. sum = 31 sum + each byte read (wrapping); a round whose
// peer is not 127.0.0.1 with a port, or whose call failed, counts as
// failed. Prints n, sum and the failed rounds. Usage: connchurn <n>.
const std = @import("std");
const net = std.Io.net;

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const any: net.IpAddress = .{ .ip4 = net.Ip4Address.loopback(0) };
    var server = try any.listen(io, .{});
    defer server.socket.close(io);
    const at = server.socket.address;
    var sum: u64 = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        const d = at.connect(io, .{ .mode = .stream }) catch {
            fails += 1;
            continue;
        };
        const s = server.accept(io) catch {
            d.close(io);
            fails += 1;
            continue;
        };
        switch (s.socket.address) {
            .ip4 => |p| {
                if (!std.mem.eql(u8, &p.bytes, &[4]u8{ 127, 0, 0, 1 }) or p.port == 0) fails += 1;
            },
            else => fails += 1,
        }
        var rb: [1]u8 = undefined;
        var wb: [2]u8 = undefined;
        var dr = d.reader(io, &rb);
        var sw = s.writer(io, &wb);
        const two = [2]u8{ @truncate(i), @truncate(i) };
        sw.interface.writeAll(&two) catch {
            fails += 1;
        };
        sw.interface.flush() catch {
            fails += 1;
        };
        const b = dr.interface.takeByte() catch blk: {
            fails += 1;
            break :blk 0;
        };
        sum = sum *% 31 +% b;
        d.close(io);
        s.close(io);
    }
    var wbuf: [64]u8 = undefined;
    var out = std.Io.File.stdout().writer(io, &wbuf);
    try out.interface.print("{d} {d} {d}\n", .{ n, sum, fails });
    try out.interface.flush();
}
