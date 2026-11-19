// grep, Zig, std: the task of README.md for the literal variants (lit and
// ci; Zig's std has no regex, so re has no Zig cell). The walk is mgrep's,
// with Io.Dir.iterate: each directory's entries collected, names starting
// with "." dropped, the rest sorted bytewise (std.mem.lessThan), then
// visited depth first, files and directories interleaved. A file is read
// whole with Dir.readFileAlloc into an arena that is reset after each file.
// A file with a NUL byte in its first 1,024 bytes is skipped as binary;
// every other file prints "path:count", the number of its lines that hold a
// match. A file that is valid UTF-8 (std.unicode.utf8ValidateSlice) is
// searched whole: the next match from the current line's start is found
// with std.mem.findPos (or std.ascii.findIgnoreCasePos for -i, which folds
// ASCII case only, as mgrep's literal search does), its line counted, and
// the search goes on at the next line. A file that is not has its lines
// judged one by one, and a line that is not valid UTF-8 is skipped.
// Usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR.
const std = @import("std");
const Io = std.Io;

const sniff = 1024;

// The bytes that make a pattern a regex (mgrep's MetaByte).
const meta = "\\.+*?()|[]{}^$";

const Ctx = struct {
    io: Io,
    names: std.mem.Allocator, // paths and listings, kept for the run, as mgrep's
    files: *std.heap.ArenaAllocator, // one file's bytes, reset after each
    out: *Io.Writer,
    err: *Io.Writer,
    needle: []const u8,
    fold: bool,
    matched: bool = false,
    failed: bool = false,
};

const Item = struct {
    name: []const u8,
    dir: bool,
};

fn usage() noreturn {
    std.debug.print("usage: grep -c -j 1 --path-separator / [-F|-i] PATTERN DIR\n", .{});
    std.process.exit(2);
}

pub fn main(init: std.process.Init) !void {
    const names = init.arena.allocator();
    const args = try init.minimal.args.toSlice(names);
    const io = init.io;
    var fixed = false;
    var fold = false;
    var counts = false;
    var rest: [2][]const u8 = undefined;
    var nrest: usize = 0;
    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        const a = args[i];
        if (std.mem.eql(u8, a, "-c")) {
            counts = true;
        } else if (std.mem.eql(u8, a, "-F")) {
            fixed = true;
        } else if (std.mem.eql(u8, a, "-i")) {
            fold = true;
        } else if (std.mem.eql(u8, a, "-j")) {
            i += 1;
            if (i == args.len or !std.mem.eql(u8, args[i], "1")) usage();
        } else if (std.mem.eql(u8, a, "--path-separator")) {
            i += 1;
            if (i == args.len or !std.mem.eql(u8, args[i], "/")) usage();
        } else if (a.len > 0 and a[0] == '-') {
            usage();
        } else {
            if (nrest == 2) usage();
            rest[nrest] = a;
            nrest += 1;
        }
    }
    if (!counts or nrest != 2) usage();
    const pat = rest[0];
    const root = rest[1];
    var ascii = true;
    for (pat) |b| ascii = ascii and b < 0x80;
    const literal = pat.len > 0 and (fixed or std.mem.findAny(u8, pat, meta) == null) and (!fold or ascii);
    if (!literal) {
        std.debug.print("grep: a regex, which Zig's std cannot match (the re variant has no Zig cell)\n", .{});
        std.process.exit(2);
    }
    var needle = pat;
    if (fold) needle = std.ascii.lowerString(try names.alloc(u8, pat.len), pat);

    var obuf: [64 << 10]u8 = undefined;
    var ow = Io.File.stdout().writer(io, &obuf);
    var ebuf: [4096]u8 = undefined;
    var ew = Io.File.stderr().writer(io, &ebuf);
    var files = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer files.deinit();
    var ctx: Ctx = .{ .io = io, .names = names, .files = &files, .out = &ow.interface, .err = &ew.interface, .needle = needle, .fold = fold };

    const shown = try std.mem.replaceOwned(u8, names, root, "\\", "/");
    if (Io.Dir.cwd().openDir(io, root, .{ .iterate = true })) |d| {
        defer d.close(io);
        try walk(&ctx, d, shown);
    } else |e| {
        try ctx.err.print("grep: {s}: {t}\n", .{ shown, e });
        ctx.failed = true;
    }
    try ctx.out.flush();
    try ctx.err.flush();
    if (ctx.failed) std.process.exit(2);
    if (!ctx.matched) std.process.exit(1);
}

fn lessThan(_: void, a: Item, b: Item) bool {
    return std.mem.lessThan(u8, a.name, b.name);
}

// The printed name of leaf inside the directory printed as name.
fn joined(a: std.mem.Allocator, name: []const u8, leaf: []const u8) ![]const u8 {
    if (std.mem.endsWith(u8, name, "/") or std.mem.endsWith(u8, name, "\\")) return std.mem.concat(a, u8, &.{ name, leaf });
    return std.mem.concat(a, u8, &.{ name, "/", leaf });
}

// Searches every file under dir, printed as name, in mgrep's order.
fn walk(ctx: *Ctx, dir: Io.Dir, name: []const u8) !void {
    const io = ctx.io;
    var items: std.ArrayList(Item) = .empty;
    var it = dir.iterate();
    while (true) {
        const e = it.next(io) catch |err| {
            try ctx.err.print("grep: {s}: listing stopped early: {t}\n", .{ name, err });
            ctx.failed = true;
            break;
        } orelse break;
        if (e.name.len > 0 and e.name[0] == '.') continue;
        // A file system that fills no d_type gives .unknown: stat the
        // entry for its kind, as grep.c does for DT_UNKNOWN (a failed stat
        // leaves it a file, whose read then reports the error).
        var is_dir = e.kind == .directory;
        if (e.kind == .unknown) {
            is_dir = if (dir.statFile(io, e.name, .{})) |st| st.kind == .directory else |_| false;
        }
        try items.append(ctx.names, .{ .name = try ctx.names.dupe(u8, e.name), .dir = is_dir });
    }
    std.mem.sort(Item, items.items, {}, lessThan);
    for (items.items) |item| {
        const shown = try joined(ctx.names, name, item.name);
        if (item.dir) {
            const sub = dir.openDir(io, item.name, .{ .iterate = true }) catch |err| {
                try ctx.err.print("grep: {s}: {t}\n", .{ shown, err });
                ctx.failed = true;
                continue;
            };
            defer sub.close(io);
            try walk(ctx, sub, shown);
            continue;
        }
        defer _ = ctx.files.reset(.retain_capacity);
        const buf = dir.readFileAlloc(io, item.name, ctx.files.allocator(), .unlimited) catch |err| {
            try ctx.err.print("grep: {s}: {t}\n", .{ shown, err });
            ctx.failed = true;
            continue;
        };
        if (std.mem.findScalar(u8, buf[0..@min(buf.len, sniff)], 0) != null) continue;
        const n = count(ctx, buf);
        if (n > 0) ctx.matched = true;
        try ctx.out.print("{s}:{d}\n", .{ shown, n });
    }
}

// The number of lines of buf that hold a match. A buffer that is not valid
// UTF-8 is judged line by line, and its invalid lines are skipped.
fn count(ctx: *Ctx, buf: []const u8) usize {
    if (std.unicode.utf8ValidateSlice(buf)) return lines(ctx, buf);
    var n: usize = 0;
    var pos: usize = 0;
    while (pos < buf.len) {
        const end = std.mem.findScalarPos(u8, buf, pos, '\n') orelse buf.len;
        var line = buf[pos..end];
        if (line.len > 0 and line[line.len - 1] == '\r') line = line[0 .. line.len - 1];
        if (std.unicode.utf8ValidateSlice(line) and lines(ctx, line) > 0) n += 1;
        pos = end + 1;
    }
    return n;
}

// The lines of buf that hold a match: the next match from the start of the
// current line, then on from the line after it. A literal holds no line
// break, so a match never spans two lines.
fn lines(ctx: *Ctx, buf: []const u8) usize {
    var n: usize = 0;
    var pos: usize = 0;
    while (pos < buf.len) {
        const at = (if (ctx.fold)
            std.ascii.findIgnoreCasePos(buf, pos, ctx.needle)
        else
            std.mem.findPos(u8, buf, pos, ctx.needle)) orelse break;
        n += 1;
        pos = (std.mem.findScalarPos(u8, buf, at, '\n') orelse break) + 1;
    }
    return n;
}
