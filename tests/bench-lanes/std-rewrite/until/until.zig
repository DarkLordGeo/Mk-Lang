// until, Zig: split a file into records through std.Io.File.Reader (64 KiB
// buffer) with std.Io.Reader.takeDelimiterInclusive (no record reaches the
// buffer's size); at the end of the stream the bytes left without a
// delimiter are the last record. Once on '\n' and once on ' ', r times;
// prints the records and the bytes taken (each record with its
// delimiter). Usage: until <file> <r>.
const std = @import("std");

const Count = struct { n: i64, bytes: i64 };

fn split(io: std.Io, p: []const u8, delim: u8, buf: []u8) !Count {
    const f = try std.Io.Dir.cwd().openFile(io, p, .{});
    defer f.close(io);
    var fr = f.reader(io, buf);
    const r = &fr.interface;
    var c: Count = .{ .n = 0, .bytes = 0 };
    while (true) {
        const s = r.takeDelimiterInclusive(delim) catch |err| switch (err) {
            error.EndOfStream => {
                const rest = r.buffered();
                if (rest.len > 0) {
                    c.n += 1;
                    c.bytes += @intCast(rest.len);
                    r.toss(rest.len);
                }
                break;
            },
            else => |e| return e,
        };
        c.n += 1;
        c.bytes += @intCast(s.len);
    }
    return c;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    const buf = try std.heap.page_allocator.alloc(u8, 65536);
    defer std.heap.page_allocator.free(buf);
    var n: i64 = 0;
    var bytes: i64 = 0;
    for (0..reps) |_| {
        for ([_]u8{ '\n', ' ' }) |d| {
            const s = try split(init.io, args[1], d, buf);
            n += s.n;
            bytes += s.bytes;
        }
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d} {d}\n", .{ n, bytes });
    try w.interface.flush();
}
