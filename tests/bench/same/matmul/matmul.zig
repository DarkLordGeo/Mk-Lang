// matmul, Zig: the reference algorithm of README.md.
// Three flat []f64 of n * n from std.heap.page_allocator, row-major, the
// i-k-j loop indexed c[i * n + j], repeated `rounds` times into the same c.
// usize indices: + and * checked in ReleaseSafe, UB in ReleaseFast. The fill
// uses +% and *% because splitmix64 wraps by design. No @setRuntimeSafety,
// no @setFloatMode, no libc.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const a = try pa.alloc(f64, n * n);
    const b = try pa.alloc(f64, n * n);
    const c = try pa.alloc(f64, n * n);
    @memset(c, 0.0);
    for (0..n * n) |i| {
        st +%= 0x9e3779b97f4a7c15;
        a[i] = @as(f64, @floatFromInt(mix(st) & 255)) / 256.0;
        st +%= 0x9e3779b97f4a7c15;
        b[i] = @as(f64, @floatFromInt(mix(st) & 255)) / 256.0;
    }
    for (0..rounds) |_| {
        for (0..n) |i| {
            for (0..n) |k| {
                const aik = a[i * n + k];
                for (0..n) |j| {
                    c[i * n + j] = c[i * n + j] + aik * b[k * n + j];
                }
            }
        }
    }
    var total: f64 = 0.0;
    for (0..n * n) |i| {
        total = total + c[i];
    }
    const sum: i64 = @intFromFloat(total * 65536.0);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
