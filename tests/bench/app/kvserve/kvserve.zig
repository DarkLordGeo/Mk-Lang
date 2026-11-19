// kvserve, Zig, std: the task of README.md, the subset of kvd's line
// protocol the load uses (PING, GET, SET, INCR, DEL, QUIT) over TCP, every
// change appended to a write-ahead log in kvd's format before it is
// answered. Usage: kvserve serve --addr HOST:PORT --data DIR [--sync
// always|never|N]. Serves until killed. One Io.Group.concurrent task per
// connection over std.Io.net, which std.Io.Threaded runs on its own OS
// thread, so the cell is labelled OS threads, as the http row's. Each
// connection reads through a std.Io.Reader whose buffer is the longest
// line (kvd's limit, 65,856 bytes, the size of kvd's own line buffer), so
// takeDelimiterInclusive returns a whole line, and writes through a 4 KiB
// std.Io.Writer flushed after every reply. The store is a
// std.StringHashMapUnmanaged (Wyhash) of heap copies from the process's
// general-purpose allocator behind a std.Io.RwLock; the log is a
// std.Io.File created for writing behind a std.Io.Mutex, each record
// written whole by one writeStreamingAll and checked by std.hash.Crc32. A
// change takes the log's lock, reads the store where it depends on it
// (DEL, INCR), appends its record, syncs it as the policy says, then takes
// the store's lock and applies it, as kvd's Change does, so the log's
// order is the store's.
const std = @import("std");
const Io = std.Io;
const Allocator = std.mem.Allocator;

const max_key = 256;
const max_value = 65536;
const line_limit = max_key + max_value + 64;
const op_set: u8 = 1;
const op_del: u8 = 2;

const Server = struct {
    gpa: Allocator,
    store_lock: Io.RwLock = .init,
    store: std.StringHashMapUnmanaged([]u8) = .empty,
    log_lock: Io.Mutex = .init,
    log: Io.File,
    every: usize, // 0: never sync; 1: each change; n: every n changes
    pending: usize = 0,
    rec: std.ArrayList(u8) = .empty,
};

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len < 2 or !std.mem.eql(u8, args[1], "serve")) return usage("usage: kvserve serve --addr HOST:PORT --data DIR [--sync always|never|N]");
    var addr: []const u8 = "127.0.0.1:7379";
    var dir: []const u8 = "kvd-data";
    var sync: []const u8 = "always";
    var i: usize = 2;
    while (i < args.len) : (i += 2) {
        if (i + 1 >= args.len) return usage("a flag needs a value");
        if (std.mem.eql(u8, args[i], "--addr")) {
            addr = args[i + 1];
        } else if (std.mem.eql(u8, args[i], "--data")) {
            dir = args[i + 1];
        } else if (std.mem.eql(u8, args[i], "--sync")) {
            sync = args[i + 1];
        } else return usage("unknown flag");
    }
    var every: usize = 1;
    if (std.mem.eql(u8, sync, "never")) {
        every = 0;
    } else if (!std.mem.eql(u8, sync, "always")) {
        every = std.fmt.parseInt(usize, sync, 10) catch return usage("--sync takes always, never or a number of changes");
        if (every < 1 or every > 1_000_000) return usage("--sync takes always, never or a number of changes");
    }
    var server: Server = .{ .gpa = init.gpa, .log = try openLog(io, dir), .every = every };
    const address = try Io.net.IpAddress.parseLiteral(addr);
    var listener = try address.listen(io, .{ .reuse_address = true, .kernel_backlog = 2048 });
    std.debug.print("kvserve: listening on {s}\n", .{addr});
    var group: Io.Group = .init;
    defer group.cancel(io);
    while (true) {
        const stream = try listener.accept(io);
        group.concurrent(io, serve, .{ io, &server, stream }) catch stream.close(io);
    }
}

fn usage(msg: []const u8) void {
    std.debug.print("kvserve: {s}\n", .{msg});
    std.process.exit(2);
}

/// A fresh log in dir: the 24-byte header of generation 0, not shut down
/// cleanly, synced. Recovery is kvd's; a twin starts empty, so it refuses
/// a directory that already holds a log.
fn openLog(io: Io, dir: []const u8) !Io.File {
    const cwd = Io.Dir.cwd();
    try cwd.createDirPath(io, dir);
    var d = try cwd.openDir(io, dir, .{});
    defer d.close(io);
    const f = try d.createFile(io, "kvd.wal", .{ .exclusive = true, .truncate = false });
    var h: [24]u8 = @splat(0);
    @memcpy(h[0..8], "KVDWAL1\n");
    std.mem.writeInt(u32, h[16..20], std.hash.Crc32.hash(h[0..16]), .little);
    try f.writeStreamingAll(io, &h);
    try f.sync(io);
    return f;
}

/// One record (length, CRC-32 of the payload, payload: operation, key
/// length, key, value), synced as the policy says. The caller holds the
/// log's lock.
fn append(s: *Server, io: Io, op: u8, key: []const u8, value: []const u8) !void {
    const n = 3 + key.len + value.len;
    s.rec.clearRetainingCapacity();
    try s.rec.ensureTotalCapacity(s.gpa, 8 + n);
    var head: [11]u8 = undefined;
    std.mem.writeInt(u32, head[0..4], @intCast(n), .little);
    head[8] = op;
    std.mem.writeInt(u16, head[9..11], @intCast(key.len), .little);
    s.rec.appendSliceAssumeCapacity(&head);
    s.rec.appendSliceAssumeCapacity(key);
    s.rec.appendSliceAssumeCapacity(value);
    std.mem.writeInt(u32, s.rec.items[4..8], std.hash.Crc32.hash(s.rec.items[8..]), .little);
    try s.log.writeStreamingAll(io, s.rec.items);
    if (s.every > 0) {
        s.pending += 1;
        if (s.pending >= s.every) {
            s.pending = 0;
            try s.log.sync(io);
        }
    }
}

/// Logs a change, then applies it to the store; false when the log or
/// the allocator refused it (the reply is then the error).
fn apply(s: *Server, io: Io, w: *Io.Writer, op: u8, key: []const u8, value: []const u8) !bool {
    append(s, io, op, key, value) catch |e| {
        try w.print("-ERR {s}\n", .{@errorName(e)});
        return false;
    };
    try s.store_lock.lock(io);
    defer s.store_lock.unlock(io);
    if (op == op_del) {
        if (s.store.fetchRemove(key)) |kv| {
            s.gpa.free(kv.key);
            s.gpa.free(kv.value);
        }
        return true;
    }
    const v = s.gpa.dupe(u8, value) catch {
        try w.writeAll("-ERR OutOfMemory\n");
        return false;
    };
    const got = s.store.getOrPut(s.gpa, key) catch {
        s.gpa.free(v);
        try w.writeAll("-ERR OutOfMemory\n");
        return false;
    };
    if (got.found_existing) {
        s.gpa.free(got.value_ptr.*);
    } else {
        got.key_ptr.* = s.gpa.dupe(u8, key) catch {
            s.store.removeByPtr(got.key_ptr);
            s.gpa.free(v);
            try w.writeAll("-ERR OutOfMemory\n");
            return false;
        };
    }
    got.value_ptr.* = v;
    return true;
}

/// Runs one request and writes its reply; true is QUIT.
fn answer(s: *Server, io: Io, w: *Io.Writer, text: []const u8) !bool {
    const sp = std.mem.indexOfScalar(u8, text, ' ');
    const word = if (sp) |k| text[0..k] else text;
    const rest = if (sp) |k| text[k + 1 ..] else "";
    if (word.len == 0) {
        try w.writeAll("-ERR Empty\n");
    } else if (is(word, "PING")) {
        try w.writeAll(if (rest.len > 0) "-ERR ExtraArgument\n" else "+PONG\n");
    } else if (is(word, "GET")) {
        const key = (try keyOnly(w, rest)) orelse return false;
        try s.store_lock.lockShared(io);
        defer s.store_lock.unlockShared(io);
        if (s.store.get(key)) |v| {
            try w.writeAll("$");
            try w.writeAll(v);
            try w.writeAll("\n");
        } else try w.writeAll("_\n");
    } else if (is(word, "SET")) {
        const k = std.mem.indexOfScalar(u8, rest, ' ');
        const key = if (k) |x| rest[0..x] else rest;
        const value = if (k) |x| rest[x + 1 ..] else "";
        if (!try checkKey(w, key) or !try checkValue(w, value)) return false;
        try s.log_lock.lock(io);
        defer s.log_lock.unlock(io);
        if (try apply(s, io, w, op_set, key, value)) try w.writeAll("+OK\n");
    } else if (is(word, "INCR")) {
        const key = (try keyOnly(w, rest)) orelse return false;
        try s.log_lock.lock(io);
        defer s.log_lock.unlock(io);
        var n: i64 = 0;
        {
            try s.store_lock.lockShared(io);
            defer s.store_lock.unlockShared(io);
            if (s.store.get(key)) |v| {
                n = decimal(v) orelse {
                    try w.writeAll("-ERR NotAnInteger\n");
                    return false;
                };
            }
        }
        var buf: [24]u8 = undefined;
        const next = std.fmt.bufPrint(&buf, "{d}", .{n + 1}) catch unreachable;
        if (try apply(s, io, w, op_set, key, next)) try w.print(":{s}\n", .{next});
    } else if (is(word, "DEL")) {
        const key = (try keyOnly(w, rest)) orelse return false;
        try s.log_lock.lock(io);
        defer s.log_lock.unlock(io);
        const found = blk: {
            try s.store_lock.lockShared(io);
            defer s.store_lock.unlockShared(io);
            break :blk s.store.contains(key);
        };
        if (!found) {
            try w.writeAll(":0\n");
        } else if (try apply(s, io, w, op_del, key, "")) try w.writeAll(":1\n");
    } else if (is(word, "QUIT")) {
        if (rest.len > 0) {
            try w.writeAll("-ERR ExtraArgument\n");
        } else {
            try w.writeAll("+BYE\n");
            return true;
        }
    } else try w.writeAll("-ERR UnknownCommand\n");
    return false;
}

/// kvd's commands are matched in upper or lower case, not mixed.
/// An optional + or - and then decimal digits, nothing else, in i64: what
/// kvd's str.ParseInt accepts. std.fmt.parseInt also takes a '_' between
/// digits, so it would read "1_000", which kvd refuses.
fn decimal(s: []const u8) ?i64 {
    var i: usize = 0;
    var neg = false;
    if (s.len > 0 and (s[0] == '+' or s[0] == '-')) {
        neg = s[0] == '-';
        i = 1;
    }
    if (i == s.len) return null;
    const limit: u64 = if (neg) 1 << 63 else (1 << 63) - 1;
    var v: u64 = 0;
    for (s[i..]) |b| {
        if (b < '0' or b > '9') return null;
        const d: u64 = b - '0';
        if (v > (limit - d) / 10) return null;
        v = v * 10 + d;
    }
    return if (neg) @bitCast(0 -% v) else @intCast(v);
}

fn is(word: []const u8, upper: []const u8) bool {
    if (std.mem.eql(u8, word, upper)) return true;
    if (word.len != upper.len) return false;
    for (word, upper) |c, u| {
        if (c != std.ascii.toLower(u)) return false;
    }
    return true;
}

/// A command's one key, with nothing after it; null when the reply is an
/// error, already written.
fn keyOnly(w: *Io.Writer, rest: []const u8) !?[]const u8 {
    const k = std.mem.indexOfScalar(u8, rest, ' ');
    if (k != null) {
        try w.writeAll("-ERR ExtraArgument\n");
        return null;
    }
    return if (try checkKey(w, rest)) rest else null;
}

/// Non-empty, at most max_key bytes, no space or control byte.
fn checkKey(w: *Io.Writer, key: []const u8) !bool {
    if (key.len == 0) {
        try w.writeAll("-ERR MissingArgument\n");
        return false;
    }
    if (key.len > max_key) {
        try w.print("-ERR KeyTooLong(len: {d}, limit: {d})\n", .{ key.len, max_key });
        return false;
    }
    for (key, 0..) |c, i| {
        if (c <= ' ' or c == 0x7F) {
            try w.print("-ERR BadByte(offset: {d})\n", .{i});
            return false;
        }
    }
    return true;
}

/// At most max_value bytes, no line break.
fn checkValue(w: *Io.Writer, value: []const u8) !bool {
    if (value.len > max_value) {
        try w.print("-ERR ValueTooLong(len: {d}, limit: {d})\n", .{ value.len, max_value });
        return false;
    }
    if (std.mem.indexOfAny(u8, value, "\r\n")) |i| {
        try w.print("-ERR BadByte(offset: {d})\n", .{i});
        return false;
    }
    return true;
}

/// Answers one connection's lines until it leaves. A line cut off by the
/// end of the stream is never run.
fn serve(io: Io, s: *Server, stream: Io.net.Stream) void {
    defer stream.close(io);
    const recv = s.gpa.alloc(u8, line_limit) catch return;
    defer s.gpa.free(recv);
    var send: [4096]u8 = undefined;
    var r = stream.reader(io, recv);
    var w = stream.writer(io, &send);
    while (true) {
        const line = r.interface.takeDelimiterInclusive('\n') catch |e| {
            if (e == error.StreamTooLong) {
                w.interface.writeAll("-ERR LineTooLong\n") catch {};
                w.interface.flush() catch {};
            }
            return;
        };
        const text = std.mem.trim(u8, line[0 .. line.len - 1], " \t\r\n");
        const quit = answer(s, io, &w.interface, text) catch return;
        w.interface.flush() catch return;
        if (quit) return;
    }
}
