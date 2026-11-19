// dispatchalt, Zig, same algorithm: ../README.md's loop of n dynamic calls
// through a two-element array of {ptr: *anyopaque, step: *const fn}, the
// C column's layout (the function pointer inline, no vtable), over an Add
// and a Xor, the target alternating (i & 1), so the indirect branch is predicted (the argument still from splitmix64): one load of the
// function pointer, one indirect call. u64 with +% and *%. Usage: dispatchalt
// <n> <seed>. Prints the checksum.
const std = @import("std");

const Step = struct { ptr: *anyopaque, step: *const fn (*anyopaque, u64) u64 };

const Add = struct {
    acc: u64,
    fn step(p: *anyopaque, x: u64) u64 {
        const a: *Add = @ptrCast(@alignCast(p));
        a.acc +%= x;
        return a.acc;
    }
};

const Xor = struct {
    acc: u64,
    fn step(p: *anyopaque, x: u64) u64 {
        const o: *Xor = @ptrCast(@alignCast(p));
        o.acc = (o.acc ^ x) *% 0x100000001b3;
        return o.acc;
    }
};

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn run(n: usize, seed: u64) u64 {
    var a = Add{ .acc = 1 };
    var x = Xor{ .acc = 0xcbf29ce484222325 };
    const objs = [2]Step{ .{ .ptr = &a, .step = Add.step }, .{ .ptr = &x, .step = Xor.step } };
    var st = seed;
    var sum: u64 = 0;
    for (0..n) |i| {
        st +%= 0x9e3779b97f4a7c15;
        const z = mix(st);
        const s = objs[i & 1];
        sum +%= s.step(s.ptr, z >> 1);
    }
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const seed = try std.fmt.parseInt(u64, args[2], 10);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{run(n, seed)});
    try w.interface.flush();
}
