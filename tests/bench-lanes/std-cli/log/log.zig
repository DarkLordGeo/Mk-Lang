// log, Zig: std.log.info with a custom std_options.logFn that writes the
// level word, the formatted message and one newline into one
// std.Io.Writer.Allocating (std.log's own hook: its defaultLog writes to
// stderr only), n lines of two integer holes per rep, the buffer cleared
// (capacity kept) before each rep, reps times (README.md). The format is
// comptime, as std.log's. No libc.
const std = @import("std");

var sink: std.Io.Writer.Allocating = undefined;

fn logFn(
    comptime level: std.log.Level,
    comptime scope: @EnumLiteral(),
    comptime format: []const u8,
    args: anytype,
) void {
    _ = scope;
    const word = comptime switch (level) {
        .err => "ERROR ",
        .warn => "WARN ",
        .info => "INFO ",
        .debug => "DEBUG ",
    };
    sink.writer.print(word ++ format ++ "\n", args) catch @panic("out of memory");
}

pub const std_options: std.Options = .{
    .log_level = .info,
    .logFn = logFn,
};

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    sink = std.Io.Writer.Allocating.init(std.heap.page_allocator);
    defer sink.deinit();
    var total: u64 = 0;
    for (0..reps) |_| {
        sink.clearRetainingCapacity();
        for (0..n) |k| {
            std.log.info("event {d} took {d} us", .{ k, (k * 7) % 1000 });
        }
        total +%= sink.written().len;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
