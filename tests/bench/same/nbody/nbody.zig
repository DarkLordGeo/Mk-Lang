// nbody, Zig: the reference algorithm of README.md.
// Five bodies in seven [5]f64 arrays of one struct, indexed s.x[i], every
// operation in the README's order; @sqrt (one sqrtsd); Zig never contracts
// a*b+c (no @setFloatMode). @intFromFloat truncates toward zero. No libc.
const std = @import("std");

const bodies = 5;
const dt: f64 = 0.0078125;

const State = struct {
    x: [bodies]f64,
    y: [bodies]f64,
    z: [bodies]f64,
    vx: [bodies]f64,
    vy: [bodies]f64,
    vz: [bodies]f64,
    m: [bodies]f64,
};

fn energy(s: *const State) f64 {
    var e: f64 = 0.0;
    for (0..bodies) |i| {
        e = e + (0.5 * s.m[i]) * ((s.vx[i] * s.vx[i] + s.vy[i] * s.vy[i]) + s.vz[i] * s.vz[i]);
        for (i + 1..bodies) |j| {
            const dx = s.x[i] - s.x[j];
            const dy = s.y[i] - s.y[j];
            const dz = s.z[i] - s.z[j];
            e = e - (s.m[i] * s.m[j]) / @sqrt((dx * dx + dy * dy) + dz * dz);
        }
    }
    return e;
}

fn advance(s: *State, steps: usize) void {
    for (0..steps) |_| {
        for (0..bodies) |i| {
            for (i + 1..bodies) |j| {
                const dx = s.x[i] - s.x[j];
                const dy = s.y[i] - s.y[j];
                const dz = s.z[i] - s.z[j];
                const d2 = (dx * dx + dy * dy) + dz * dz;
                const mag = dt / (d2 * @sqrt(d2));
                s.vx[i] = s.vx[i] - (dx * s.m[j]) * mag;
                s.vy[i] = s.vy[i] - (dy * s.m[j]) * mag;
                s.vz[i] = s.vz[i] - (dz * s.m[j]) * mag;
                s.vx[j] = s.vx[j] + (dx * s.m[i]) * mag;
                s.vy[j] = s.vy[j] + (dy * s.m[i]) * mag;
                s.vz[j] = s.vz[j] + (dz * s.m[i]) * mag;
            }
        }
        for (0..bodies) |i| {
            s.x[i] = s.x[i] + dt * s.vx[i];
            s.y[i] = s.y[i] + dt * s.vy[i];
            s.z[i] = s.z[i] + dt * s.vz[i];
        }
    }
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const steps = try std.fmt.parseInt(usize, args[1], 10);
    var s = State{
        .x = .{ 0.0, 4.0, -2.0, 6.0, -9.0 },
        .y = .{ 0.0, 0.0, 0.0, 0.0, 0.0 },
        .z = .{ 0.0, 0.25, -0.125, 0.5, 0.0 },
        .vx = .{ 0.0, 0.0, 0.0, 0.0, 0.0 },
        .vy = .{ 0.0, 1.375, -2.0, 1.125, -0.9375 },
        .vz = .{ 0.0, 0.0, 0.0625, 0.0, 0.03125 },
        .m = .{ 8.0, 0.03125, 0.015625, 0.0625, 0.0078125 },
    };
    advance(&s, steps);
    const e: i64 = @intFromFloat(energy(&s) * 1e9);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{e});
    try w.interface.flush();
}
