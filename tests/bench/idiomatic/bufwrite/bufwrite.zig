// bufwrite, Zig, idiomatic std: the task of README.md with std.Io: a file
// made by Dir.createFile in the temporary directory (TMP, then TEMP, on
// Windows; TMPDIR or /tmp elsewhere, as std-fs2/fs's Zig cell finds it),
// File.writer over a 64 KiB array, print of each line ("{d} {x} {d}\n"),
// flush and close; then Dir.readFileAlloc, FNV-1a over its bytes, and
// Dir.deleteFile. Usage: bufwrite <n>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const gpa = init.arena.allocator();
    const args = try init.minimal.args.toSlice(gpa);
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const env = init.environ_map;
    const tmp = if (@import("builtin").os.tag == .windows)
        env.get("TMP") orelse env.get("TEMP") orelse "."
    else
        env.get("TMPDIR") orelse "/tmp";
    const p = try std.fmt.allocPrint(gpa, "{s}/mkbench-bufwrite-zig.txt", .{tmp});
    const cwd = std.Io.Dir.cwd();
    {
        const f = try cwd.createFile(io, p, .{});
        defer f.close(io);
        var buf: [65536]u8 = undefined;
        var w = f.writer(io, &buf);
        var u: u64 = 0;
        while (u < n) : (u += 1) {
            const h = u *% 0x9e3779b97f4a7c15;
            try w.interface.print("{d} {x} {d}\n", .{ u, h, u * 7 });
        }
        try w.interface.flush();
    }
    const pa = std.heap.page_allocator;
    const b = try cwd.readFileAlloc(io, p, pa, .unlimited);
    defer pa.free(b);
    var h: u64 = 0xcbf29ce484222325;
    for (b) |c| h = (h ^ c) *% 0x100000001b3;
    const total = @as(u64, b.len) +% h;
    try cwd.deleteFile(io, p);
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
