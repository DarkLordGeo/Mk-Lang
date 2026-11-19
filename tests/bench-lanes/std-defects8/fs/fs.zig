// fs, Zig: the tree operations of tests/bench-lanes/std-fs2/README.md
// through std.Io.Dir. The tree is made under the temporary directory (TMP,
// TEMP or USERPROFILE on Windows, TMPDIR or /tmp on Linux, as the other
// cells pick it; Zig's std names none) in mkbench-sd8-zig: 10
// directories d<i>, each with 10 directories s<j> of 10 one-byte files f<k>,
// 1,110 entries.
// Usage: fs walk <reps> | fs mkrm <reps> | fs copy <file> <reps>.
//   walk: makes the tree, lists it reps times with Dir.walk (the root not
//         counted), removes it; prints reps, directories, files and name
//         bytes, summed over the walks.
//   mkrm: reps times makes the tree (Dir.createDirPath and Dir.writeFile)
//         and removes it with Dir.deleteTree; prints reps, entries made and
//         the removals that left the root behind.
//   copy: copies file reps times with Dir.copyFile onto one destination;
//         prints reps and the bytes copied (the destination's size).
const std = @import("std");
const Io = std.Io;

fn makeTree(io: Io, root: []const u8) !u64 {
    var buf: [4096]u8 = undefined;
    var n: u64 = 0;
    const cwd = Io.Dir.cwd();
    for (0..10) |i| {
        for (0..10) |j| {
            const d = try std.fmt.bufPrint(&buf, "{s}/d{d}/s{d}", .{ root, i, j });
            try cwd.createDirPath(io, d);
            for (0..10) |k| {
                var fbuf: [4096]u8 = undefined;
                const f = try std.fmt.bufPrint(&fbuf, "{s}/f{d}", .{ d, k });
                try cwd.writeFile(io, .{ .sub_path = f, .data = "x" });
            }
            n += 11;
        }
        n += 1;
    }
    return n;
}

fn exists(io: Io, p: []const u8) bool {
    _ = Io.Dir.cwd().statFile(io, p, .{ .follow_symlinks = false }) catch return false;
    return true;
}

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const gpa = init.arena.allocator();
    const args = try init.minimal.args.toSlice(gpa);
    if (args.len < 3) return error.Usage;
    const env = init.environ_map;
    // The other cells' order: GetTempPath2W's on Windows (TMP, TEMP,
    // USERPROFILE), TMPDIR then /tmp on Linux, so all five trees share a
    // volume.
    const tmp = if (@import("builtin").os.tag == .windows)
        env.get("TMP") orelse env.get("TEMP") orelse env.get("USERPROFILE") orelse "C:\\Windows\\Temp"
    else
        env.get("TMPDIR") orelse "/tmp";
    const root = try std.fmt.allocPrint(gpa, "{s}/mkbench-sd8-zig", .{tmp});
    const cwd = Io.Dir.cwd();
    var out: [128]u8 = undefined;
    var w = Io.File.stdout().writer(io, &out);
    if (std.mem.eql(u8, args[1], "copy")) {
        if (args.len < 4) return error.Usage;
        const n = try std.fmt.parseInt(u64, args[3], 10);
        const dst = try std.fmt.allocPrint(gpa, "{s}-copy", .{root});
        var bytes: u64 = 0;
        for (0..n) |_| {
            try cwd.copyFile(args[2], cwd, dst, io, .{});
            bytes += (try cwd.statFile(io, dst, .{})).size;
        }
        try cwd.deleteFile(io, dst);
        try w.interface.print("{d} {d}\n", .{ n, bytes });
        try w.interface.flush();
        return;
    }
    const n = try std.fmt.parseInt(u64, args[2], 10);
    try cwd.deleteTree(io, root);
    if (std.mem.eql(u8, args[1], "walk")) {
        _ = try makeTree(io, root);
        var dirs: u64 = 0;
        var files: u64 = 0;
        var names: u64 = 0;
        for (0..n) |_| {
            var rd = try cwd.openDir(io, root, .{ .iterate = true });
            defer rd.close(io);
            var walker = try rd.walk(gpa);
            defer walker.deinit();
            while (try walker.next(io)) |e| {
                if (e.kind == .directory) dirs += 1 else files += 1;
                names += e.basename.len;
            }
        }
        try cwd.deleteTree(io, root);
        try w.interface.print("{d} {d} {d} {d}\n", .{ n, dirs, files, names });
        try w.interface.flush();
        return;
    }
    var made: u64 = 0;
    var fails: u64 = 0;
    for (0..n) |_| {
        made += try makeTree(io, root);
        try cwd.deleteTree(io, root);
        if (exists(io, root)) fails += 1;
    }
    try w.interface.print("{d} {d} {d}\n", .{ n, made, fails });
    try w.interface.flush();
}
