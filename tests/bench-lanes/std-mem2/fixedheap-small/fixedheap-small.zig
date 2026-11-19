// fixedheap-small, Zig: the task of ../README.md with
// std.heap.smp_allocator (Zig's general-purpose allocator for release
// builds), the Zig cell of tests/bench/idiomatic/alloc-small: each object
// is gpa.dupe(u8, src[off .. off + l]) (one allocation and the copy), kept
// in a ring of 4,096 slices; each step reads the oldest, puts a new one in
// its slot, and frees the old one. At the end every live object is read
// and freed. Usage: fixedheap-small <steps> <seed>.
const std = @import("std");

const live = 4096;

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn make(gpa: std.mem.Allocator, src: []const u8, st: *u64) ![]u8 {
    st.* +%= 0x9e3779b97f4a7c15;
    const v = mix(st.*);
    const l: usize = @intCast(v % 249 + 8);
    const off: usize = @intCast((v >> 16) % 3840);
    return gpa.dupe(u8, src[off .. off + l]);
}

fn read(o: []const u8) u64 {
    return @as(u64, o.len) +% o[0] +% o[o.len - 1];
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const steps = try std.fmt.parseInt(u64, args[1], 10);
    var st = try std.fmt.parseInt(u64, args[2], 10);
    var buf: [4096]u8 = undefined;
    var gs = st ^ 0x5555555555555555;
    for (&buf) |*b| {
        gs +%= 0x9e3779b97f4a7c15;
        b.* = @as(u8, @intCast(mix(gs) % 26)) + 'a';
    }
    const gpa = std.heap.smp_allocator;
    var ring: [live][]u8 = undefined;
    for (&ring) |*o| o.* = try make(gpa, &buf, &st);
    var total: u64 = 0;
    var j: u64 = 0;
    while (j < steps) : (j += 1) {
        const slot: usize = @intCast(j % live);
        total +%= read(ring[slot]);
        const o = try make(gpa, &buf, &st);
        gpa.free(ring[slot]);
        ring[slot] = o;
    }
    for (ring) |o| {
        total +%= read(o);
        gpa.free(o);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
