// binarytrees, Zig: the reference algorithm of README.md (the Benchmarks
// Game's binary-trees, with the row's bump allocator). Nodes are Node{ left: u32,
// right: u32 } in one []Node from std.heap.page_allocator, allocated by a u32
// bump index in a Pool; index 0 is the null node. No std allocator per node
// (not ArenaAllocator: the row's allocator is the same inline bump everywhere).
// + checked in ReleaseSafe, UB in ReleaseFast. No libc.
const std = @import("std");

const Node = struct {
    left: u32,
    right: u32,
};

const Pool = struct {
    nodes: []Node,
    top: u32,
};

fn build(p: *Pool, depth: i64) u32 {
    const idx = p.top;
    p.top += 1;
    if (depth > 0) {
        const l = build(p, depth - 1);
        const r = build(p, depth - 1);
        p.nodes[idx] = .{ .left = l, .right = r };
    } else {
        p.nodes[idx] = .{ .left = 0, .right = 0 };
    }
    return idx;
}

fn check(nodes: []const Node, idx: u32) i64 {
    const n = nodes[idx];
    if (n.left == 0) {
        return 1;
    }
    return 1 + check(nodes, n.left) + check(nodes, n.right);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(i64, args[1], 10);
    const min_depth: i64 = 4;
    var max_depth = n;
    if (min_depth + 2 > max_depth) {
        max_depth = min_depth + 2;
    }
    const cap = (@as(usize, 1) << @intCast(max_depth + 2)) + 1;
    const nodes = try std.heap.page_allocator.alloc(Node, cap);
    @memset(nodes, .{ .left = 0, .right = 0 });
    var p = Pool{ .nodes = nodes, .top = 1 };
    var total = check(p.nodes, build(&p, max_depth + 1));
    p.top = 1;
    const long_lived = build(&p, max_depth);
    const mark = p.top;
    var d = min_depth;
    while (d <= max_depth) : (d += 2) {
        const iterations = @as(i64, 1) << @intCast(max_depth - d + min_depth);
        var i: i64 = 0;
        while (i < iterations) : (i += 1) {
            p.top = mark;
            total += check(p.nodes, build(&p, d));
        }
    }
    total += check(p.nodes, long_lived);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
