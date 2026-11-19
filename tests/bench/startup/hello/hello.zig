// hello, Zig: startup only, no libc. Prints 1 and exits.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    var buf: [16]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("1\n", .{});
    try w.interface.flush();
}
