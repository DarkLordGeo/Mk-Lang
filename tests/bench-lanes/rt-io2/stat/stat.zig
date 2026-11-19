// stat, Zig: the facts of one existing file, n times, through
// std.Io.Dir.cwd().statFile (following links, its default); sum adds each
// answer's size. Prints n, sum and the failed calls. Usage: stat <path> <n>.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const p = args[1];
    const n = try std.fmt.parseInt(u64, args[2], 10);
    var sum: u64 = 0;
    var fails: u64 = 0;
    for (0..n) |_| {
        const st = std.Io.Dir.cwd().statFile(init.io, p, .{}) catch {
            fails += 1;
            continue;
        };
        sum += st.size;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d} {d} {d}\n", .{ n, sum, fails });
    try w.interface.flush();
}
