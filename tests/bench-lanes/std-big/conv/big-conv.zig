// big-conv, Zig, idiomatic std: the Mk row's loop with std.math.big.int
// (Managed): x0 is 2000 splitmix64 words of the seed; n times s =
// toString(x, 10), x = setString(s, 10) + 1; prints x's low word xor the
// sum of the texts' lengths. Every value and text uses init.gpa, Zig's
// default-selected general purpose allocator (std.process.Init).
// Usage: big-conv <n> <seed>.
const std = @import("std");
const Managed = std.math.big.int.Managed;

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    const a = init.gpa;
    var x = try Managed.initCapacity(a, 2000);
    defer x.deinit();
    var i: usize = 0;
    while (i < 2000) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        x.limbs[i] = mix(st);
    }
    var len: usize = 2000;
    while (len > 1 and x.limbs[len - 1] == 0) len -= 1;
    x.setMetadata(true, len);
    var t = try Managed.init(a);
    defer t.deinit();
    var digits: u64 = 0;
    var k: u64 = 0;
    while (k < n) : (k += 1) {
        const s = try x.toString(a, 10, .lower);
        defer a.free(s);
        try t.setString(10, s);
        try x.addScalar(&t, 1);
        digits +%= s.len;
    }
    const c = x.toConst();
    const low: u64 = if (c.eqlZero()) 0 else c.limbs[0];
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{low ^ digits});
    try w.interface.flush();
}
