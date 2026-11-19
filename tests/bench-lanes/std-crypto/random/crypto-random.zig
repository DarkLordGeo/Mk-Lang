// crypto-random, Zig: std.Io.random into one buffer of <size> bytes, <n>
// times, as mk/crypto-random.mk's comment says. Io.random is Zig's
// idiomatic CSPRNG: a per-thread ChaCha DRBG in user space, seeded once
// from randomSecure (Io/Threaded.zig). ProcessPrng, which Mk's
// crypto.Random and Go's crypto/rand.Read call, is a user-mode generator
// too (per-CPU AES state seeded from \Device\CNG, as the comment in Zig's
// Io/Threaded.zig describes it). Zig's other form, Io.randomSecure, makes
// a system call per draw; it is not this column.
const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const size = try std.fmt.parseInt(usize, args[2], 10);
    const buf = try std.heap.page_allocator.alloc(u8, size);
    defer std.heap.page_allocator.free(buf);
    var total: u64 = 0;
    for (0..n) |_| {
        init.io.random(buf);
        total +%= buf.len;
    }
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
