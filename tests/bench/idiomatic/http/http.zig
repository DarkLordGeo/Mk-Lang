// http, Zig, idiomatic std: the task of README.md with std.http.Server over
// std.Io.net, one connection per Io.Group.concurrent task, which
// std.Io.Threaded runs on its own OS thread (concurrent_limit unlimited),
// so the cell is labelled OS threads. Usage: http <port>. Serves until
// killed.
const std = @import("std");
const Io = std.Io;

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const port = try std.fmt.parseInt(u16, args[1], 10);
    const address = try Io.net.IpAddress.parse("127.0.0.1", port);
    var server = try address.listen(io, .{ .reuse_address = true, .kernel_backlog = 2048 });
    var group: Io.Group = .init;
    defer group.cancel(io);
    while (true) {
        const stream = try server.accept(io);
        group.concurrent(io, serve, .{ io, stream }) catch stream.close(io);
    }
}

fn serve(io: Io, stream: Io.net.Stream) void {
    defer stream.close(io);
    var recv: [4096]u8 = undefined;
    var send: [4096]u8 = undefined;
    var r = stream.reader(io, &recv);
    var w = stream.writer(io, &send);
    var server = std.http.Server.init(&r.interface, &w.interface);
    while (server.reader.state == .ready) {
        var request = server.receiveHead() catch return;
        request.respond("Hello, World!\n", .{
            .extra_headers = &.{.{ .name = "content-type", .value = "text/plain" }},
        }) catch return;
    }
}
