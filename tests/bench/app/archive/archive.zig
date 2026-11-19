// archive, Zig, std: the task of README.md. `create` walks the tree with
// std.Io.Dir.walk, keeps the regular files, sorts them by their relative
// path with `/` in byte order (std.sort.pdq), reads each whole with
// Dir.readFileAlloc, hashes it with std.crypto.hash.sha2.Sha256 and appends
// it to one payload ArrayList; the payload is compressed by
// std.compress.flate.Compress (raw container, Options.default, which is
// level_6) into an Io.Writer.Allocating, the manifest is signed by
// std.crypto.sign.Ed25519 (KeyPair.generateDeterministic on the fixed
// seed), and the archive body is sealed by
// std.crypto.aead.aes_gcm.Aes256Gcm under the fixed key and nonce. `open`
// reverses it: it opens the seal, verifies the signature, inflates the
// payload with std.compress.flate.Decompress over a fixed Io.Reader, checks
// every file's SHA-256 against the manifest, and prints the manifest and a
// summary line through a 64 KiB stdout writer. Zig picks its AES, GHASH and
// SHA-256 code at compile time from the target's CPU features (README.md,
// Hardware paths).
// Usage: archive create <dir> <out> [<files>] | archive open <archive>.
const std = @import("std");
const flate = std.compress.flate;
const Sha256 = std.crypto.hash.sha2.Sha256;
const Ed25519 = std.crypto.sign.Ed25519;
const Aes256Gcm = std.crypto.aead.aes_gcm.Aes256Gcm;

const magic = "MKARCH01";

// The row's fixed test material (README.md, Format): the AES-256 key is the
// bytes 0 to 31, the nonce 160 to 171 and the Ed25519 seed 64 to 95.
fn fixed(comptime n: usize, first: u8) [n]u8 {
    var b: [n]u8 = undefined;
    for (&b, 0..) |*x, i| x.* = first + @as(u8, @intCast(i));
    return b;
}

const File = struct {
    rel: []const u8,
    native: []const u8,
};

fn before(_: void, a: File, b: File) bool {
    return std.mem.order(u8, a.rel, b.rel) == .lt;
}

fn create(io: std.Io, a: std.mem.Allocator, dir_path: []const u8, out: []const u8, limit: ?usize) !void {
    var root = try std.Io.Dir.cwd().openDir(io, dir_path, .{ .iterate = true });
    defer root.close(io);
    var files: std.ArrayList(File) = .empty;
    var walker = try root.walk(a);
    defer walker.deinit();
    while (try walker.next(io)) |e| {
        if (e.kind != .file) continue;
        const native = try a.dupe(u8, e.path);
        const rel = try a.dupe(u8, e.path);
        std.mem.replaceScalar(u8, rel, '\\', '/');
        try files.append(a, .{ .rel = rel, .native = native });
    }
    std.sort.pdq(File, files.items, {}, before);
    if (limit) |n| if (n < files.items.len) files.shrinkRetainingCapacity(n);
    var manifest: std.ArrayList(u8) = .empty;
    var payload: std.ArrayList(u8) = .empty;
    for (files.items) |f| {
        const data = try root.readFileAlloc(io, f.native, a, .unlimited);
        defer a.free(data);
        var digest: [Sha256.digest_length]u8 = undefined;
        Sha256.hash(data, &digest, .{});
        try manifest.appendSlice(a, &std.fmt.bytesToHex(digest, .lower));
        try manifest.print(a, " {d} {s}\n", .{ data.len, f.rel });
        try payload.appendSlice(a, data);
    }
    // Compress asserts an output buffer of more than 8 bytes; it grows from 64 KiB.
    var packed_w: std.Io.Writer.Allocating = try .initCapacity(a, 64 << 10);
    const window = try a.alloc(u8, flate.max_window_len);
    var c = try flate.Compress.init(&packed_w.writer, window, .raw, .default);
    try c.writer.writeAll(payload.items);
    try c.finish();
    const packed_bytes = packed_w.written();
    const kp = try Ed25519.KeyPair.generateDeterministic(fixed(32, 64));
    const sig = (try kp.sign(manifest.items, null)).toBytes();
    var body: std.ArrayList(u8) = try .initCapacity(a, 8 + manifest.items.len + 64 + packed_bytes.len);
    var mlen: [8]u8 = undefined;
    std.mem.writeInt(u64, &mlen, manifest.items.len, .little);
    body.appendSliceAssumeCapacity(&mlen);
    body.appendSliceAssumeCapacity(manifest.items);
    body.appendSliceAssumeCapacity(&sig);
    body.appendSliceAssumeCapacity(packed_bytes);
    const file = try a.alloc(u8, 8 + body.items.len + Aes256Gcm.tag_length);
    @memcpy(file[0..8], magic);
    const tag = file[8 + body.items.len ..][0..Aes256Gcm.tag_length];
    Aes256Gcm.encrypt(file[8..][0..body.items.len], tag, body.items, magic, fixed(12, 160), fixed(32, 0));
    try std.Io.Dir.cwd().writeFile(io, .{ .sub_path = out, .data = file });
    var wbuf: [256]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &wbuf);
    try w.interface.print("archive: {d} files, {d} bytes\n", .{ files.items.len, payload.items.len });
    try w.interface.flush();
}

const Entry = struct {
    digest: [32]u8,
    size: usize,
};

fn open(io: std.Io, a: std.mem.Allocator, path: []const u8) !void {
    const file = try std.Io.Dir.cwd().readFileAlloc(io, path, a, .unlimited);
    if (file.len < 8 + Aes256Gcm.tag_length or !std.mem.eql(u8, file[0..8], magic)) return error.NotAnArchive;
    const sealed = file[8 .. file.len - Aes256Gcm.tag_length];
    const tag = file[file.len - Aes256Gcm.tag_length ..][0..Aes256Gcm.tag_length].*;
    const plain = try a.alloc(u8, sealed.len);
    Aes256Gcm.decrypt(plain, sealed, tag, magic, fixed(12, 160), fixed(32, 0)) catch return error.SealDoesNotOpen;
    if (plain.len < 8 + 64) return error.CutArchive;
    const mlen = std.mem.readInt(u64, plain[0..8], .little);
    if (mlen > plain.len - 8 - 64) return error.CutArchive;
    const m: usize = @intCast(mlen);
    const manifest = plain[8 .. 8 + m];
    const sig = Ed25519.Signature.fromBytes(plain[8 + m ..][0..64].*);
    const packed_bytes = plain[8 + m + 64 ..];
    const kp = try Ed25519.KeyPair.generateDeterministic(fixed(32, 64));
    sig.verify(manifest, kp.public_key) catch return error.BadSignature;
    var entries: std.ArrayList(Entry) = .empty;
    var total: usize = 0;
    var lines = std.mem.splitScalar(u8, manifest, '\n');
    while (lines.next()) |line| {
        if (line.len == 0 and lines.peek() == null) break;
        if (line.len < 67 or line[64] != ' ') return error.BadManifest;
        const sp = std.mem.indexOfScalarPos(u8, line, 65, ' ') orelse return error.BadManifest;
        var e: Entry = .{ .digest = undefined, .size = try std.fmt.parseInt(usize, line[65..sp], 10) };
        _ = try std.fmt.hexToBytes(&e.digest, line[0..64]);
        try entries.append(a, e);
        total = std.math.add(usize, total, e.size) catch return error.BadManifest;
    }
    var in: std.Io.Reader = .fixed(packed_bytes);
    const window = try a.alloc(u8, flate.max_window_len);
    var d: flate.Decompress = .init(&in, .raw, window);
    const data = try a.alloc(u8, total);
    try d.reader.readSliceAll(data);
    if (d.reader.takeByte()) |_| return error.PayloadTooLong else |err| if (err != error.EndOfStream) return err;
    var at: usize = 0;
    for (entries.items) |e| {
        var digest: [32]u8 = undefined;
        Sha256.hash(data[at..][0..e.size], &digest, .{});
        if (!std.mem.eql(u8, &digest, &e.digest)) return error.DigestDiffers;
        at += e.size;
    }
    const wbuf = try a.alloc(u8, 64 << 10);
    var w = std.Io.File.stdout().writer(io, wbuf);
    try w.interface.writeAll(manifest);
    try w.interface.print("ok: {d} files, {d} bytes\n", .{ entries.items.len, total });
    try w.interface.flush();
}

pub fn main(init: std.process.Init) !void {
    // One general-purpose allocator: the payload grows by reallocation, and
    // each file's bytes are freed once appended. Nothing else is freed
    // before the process exits.
    const a = std.heap.smp_allocator;
    const args = try init.minimal.args.toSlice(a);
    if (args.len >= 4 and args.len <= 5 and std.mem.eql(u8, args[1], "create")) {
        const limit: ?usize = if (args.len == 5) try std.fmt.parseInt(usize, args[4], 10) else null;
        return create(init.io, a, args[2], args[3], limit);
    }
    if (args.len == 3 and std.mem.eql(u8, args[1], "open")) return open(init.io, a, args[2]);
    return error.Usage;
}
