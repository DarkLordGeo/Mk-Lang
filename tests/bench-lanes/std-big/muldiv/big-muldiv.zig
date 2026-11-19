// big-muldiv, Zig, idiomatic std: the Mk row's loop with std.math.big.int
// (Managed): x0 (1024 words), y (512 words) and d (1024 words, top bit set)
// are splitmix64 words of the seed; n times p = x y (mul), (q, r) = p
// divmod d (divTrunc) and x = r + q; prints x's low word xor its word count.
// Every value uses init.gpa, Zig's default-selected general purpose
// allocator (std.process.Init: SmpAllocator in ReleaseFast), which reuses
// the library's per-call temporaries; page_allocator would map and unmap
// each one. Usage: big-muldiv <n> <seed>.
const std = @import("std");
const Managed = std.math.big.int.Managed;

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn fill(a: std.mem.Allocator, st: *u64, n: usize, top: u64) !Managed {
    var m = try Managed.initCapacity(a, n);
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st.* +%= 0x9e3779b97f4a7c15;
        m.limbs[i] = mix(st.*);
    }
    m.limbs[n - 1] |= top;
    var len = n;
    while (len > 1 and m.limbs[len - 1] == 0) len -= 1;
    m.setMetadata(true, len);
    return m;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    const a = init.gpa;
    var x = try fill(a, &st, 1024, 0);
    defer x.deinit();
    var y = try fill(a, &st, 512, 0);
    defer y.deinit();
    var d = try fill(a, &st, 1024, 1 << 63);
    defer d.deinit();
    var p = try Managed.init(a);
    defer p.deinit();
    var q = try Managed.init(a);
    defer q.deinit();
    var r = try Managed.init(a);
    defer r.deinit();
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        try p.mul(&x, &y);
        try q.divTrunc(&r, &p, &d);
        try x.add(&r, &q);
    }
    const c = x.toConst();
    const low: u64 = if (c.eqlZero()) 0 else c.limbs[0];
    const count: u64 = if (c.eqlZero()) 0 else c.limbs.len;
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{low ^ count});
    try w.interface.flush();
}
