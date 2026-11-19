// read, Zig: read a file through std.Io.File.Reader (64 KiB buffer) by
// std.Io.Reader.readSliceShort calls of want bytes each (Zig's read of up
// to n bytes: short only at the end of the stream), r times, filling the
// buffer first when want is below its size (pass's comment); prints the
// bytes read and the wrapping sum of each call's first byte. Usage: read
// <file> <want> <r>.
const std = @import("std");

const Count = struct { bytes: i64, sum: u64 };

fn pass(io: std.Io, p: []const u8, buf: []u8, d: []u8) !Count {
    const f = try std.Io.Dir.cwd().openFile(io, p, .{});
    defer f.close(io);
    var fr = f.reader(io, buf);
    const r = &fr.interface;
    var c: Count = .{ .bytes = 0, .sum = 0 };
    while (true) {
        // Zig 0.17's File.Reader reads only a vector's first slice on
        // Windows, so a 64-byte readSliceShort from an empty buffer would
        // be one read of the file per call. A request below the buffer's
        // size fills the buffer first, as Go bufio's Read does inside.
        if (d.len < buf.len and r.bufferedLen() == 0) {
            r.fillMore() catch |err| switch (err) {
                error.EndOfStream => break,
                else => |e| return e,
            };
        }
        const k = try r.readSliceShort(d);
        if (k == 0) break;
        c.bytes += @intCast(k);
        c.sum +%= d[0];
    }
    return c;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const want = try std.fmt.parseInt(usize, args[2], 10);
    const reps = try std.fmt.parseInt(usize, args[3], 10);
    const buf = try std.heap.page_allocator.alloc(u8, 65536);
    defer std.heap.page_allocator.free(buf);
    const d = try std.heap.page_allocator.alloc(u8, want);
    defer std.heap.page_allocator.free(d);
    var bytes: i64 = 0;
    var sum: u64 = 0;
    for (0..reps) |_| {
        const s = try pass(init.io, args[1], buf, d);
        bytes += s.bytes;
        sum +%= s.sum;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d} {d}\n", .{ bytes, sum });
    try w.interface.flush();
}
