// sieve, Zig: the reference algorithm of README.md.
// A []u8 of n + 1 flags from std.heap.page_allocator indexed with flags[i].
// usize indices (the type Zig indexes with), i64 counters like C's int64_t:
// + checked in ReleaseSafe, UB in ReleaseFast.
// No @setRuntimeSafety, no libc.
const std = @import("std");

// tools/bench flips this line in a copy for the counting build.
const bench_count = false;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const flags = try std.heap.page_allocator.alloc(u8, n + 1);
    defer std.heap.page_allocator.free(flags);
    var total: i64 = 0;
    var marks: u64 = 0;
    for (0..rounds) |_| {
        for (0..n + 1) |i| {
            flags[i] = 1;
        }
        var count: i64 = 0;
        for (2..n + 1) |i| {
            if (flags[i] != 0) {
                count += 1;
                var j = i * i;
                while (j <= n) {
                    flags[j] = 0;
                    if (bench_count) marks += 1;
                    j += i;
                }
            }
        }
        total += count;
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    if (bench_count) try w.interface.print("counts marks={d}\n", .{marks});
    try w.interface.flush();
}
