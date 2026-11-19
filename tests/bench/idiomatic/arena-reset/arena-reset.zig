// arena-reset, Zig, idiomatic std: the task of README.md with
// std.heap.ArenaAllocator over std.heap.page_allocator, made once before
// the rounds; per round n nodes made one at a time with create(Node), each
// linked to the one before by a pointer, the list walked from its head,
// then reset(.retain_capacity), which keeps all of the arena's capacity for
// the next round (README.md names the policy). Usage: arena-reset <n>
// <rounds> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

const Node = struct {
    val: u64,
    next: ?*const Node,
};

fn build(a: std.mem.Allocator, n: u64, st: *u64) !?*const Node {
    var head: ?*const Node = null;
    var i: u64 = 0;
    while (i < n) : (i += 1) {
        st.* +%= 0x9e3779b97f4a7c15;
        const node = try a.create(Node);
        node.* = .{ .val = mix(st.*), .next = head };
        head = node;
    }
    return head;
}

fn walk(head: ?*const Node) u64 {
    var sum: u64 = 0;
    var i: u64 = 0;
    var p = head;
    while (p) |node| {
        i +%= 1;
        sum +%= node.val *% i;
        p = node.next;
    }
    return sum +% i;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        const head = try build(arena.allocator(), n, &st);
        total +%= walk(head);
        _ = arena.reset(.retain_capacity);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
