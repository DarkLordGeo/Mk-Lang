// run, Zig, std.process.run: n runs of this program with "child" (which
// writes 64 bytes to standard output and exits 0), standard output and
// standard error captured (Io.File.MultiReader drains both at once),
// standard input ignored (the null device), each waited for. Prints n, the
// bytes captured and the failed runs. Usage: run <n>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    if (std.mem.eql(u8, args[1], "child")) {
        var buf: [128]u8 = undefined;
        var w = std.Io.File.stdout().writer(io, &buf);
        try w.interface.writeAll("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n");
        try w.interface.flush();
        return;
    }
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const me = try std.process.executablePathAlloc(io, init.arena.allocator());
    const gpa = init.gpa;
    var total: usize = 0;
    var fails: u64 = 0;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        const r = std.process.run(gpa, io, .{ .argv = &.{ me, "child" } }) catch {
            fails += 1;
            continue;
        };
        if (r.term.success()) {
            total += r.stdout.len + r.stderr.len;
        } else {
            fails += 1;
        }
        gpa.free(r.stdout);
        gpa.free(r.stderr);
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &buf);
    try w.interface.print("{d} {d} {d}\n", .{ n, total, fails });
    try w.interface.flush();
}
