// httpapi, Zig, std: the task of README.md, a JSON API over HTTP/1.1 with
// std.http.Server over std.Io.net, one connection per Io.Group.concurrent
// task, which std.Io.Threaded runs on its own OS thread, so the cell is
// labelled OS threads, as the http row's. Each connection has an 8 KiB
// read and a 4 KiB write buffer, an ArenaAllocator over the process's
// general-purpose allocator, reset after each request (one arena per
// request, as Mk's std.http.Serve), and a 64 KiB deflate window for its
// responses. Items live in a std.AutoHashMapUnmanaged from id to their
// text, heap copies, behind a std.Io.RwLock, with the next id beside
// them. Bodies are parsed by std.json into a std.json.Value (400 when they
// are not JSON; a repeated member keeps its last value) and then into the
// item by the members' JSON types (422 when it does not fit, unknown
// members ignored, as Mk's codec does); ids and limits are a sign and
// digits, as Mk's str.ParseInt reads them; bodies are written by
// std.json.Stringify; a body of 256 bytes or more goes out
// gzip-compressed (std.compress.flate.Compress, its default level) when
// the request accepts gzip. respond states the Content-Length. Usage:
// httpapi <port>. Serves 127.0.0.1:<port> until killed.
const std = @import("std");
const Io = std.Io;
const Allocator = std.mem.Allocator;
const flate = std.compress.flate;

const gzip_min = 256;
const max_name = 64;
const max_tag = 32;
const max_qty = 1_000_000_000;
const max_limit = 100;
const default_limit = 20;
const body_limit = 65536;

const ItemIn = struct { name: []const u8, tag: []const u8, qty: i64 };
const Item = struct { id: i64, name: []const u8, tag: []const u8, qty: i64 };
const ItemList = struct { items: []const Item, total: usize };
const Problem = struct { message: []const u8 };
const Deleted = struct { deleted: bool };

const Stored = struct { name: []u8, tag: []u8, qty: i64 };

const Store = struct {
    gpa: Allocator,
    lock: Io.RwLock = .init,
    items: std.AutoHashMapUnmanaged(i64, Stored) = .empty,
    next: i64 = 1,
};

pub fn main(init: std.process.Init) !void {
    const io = init.io;
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const port = try std.fmt.parseInt(u16, args[1], 10);
    var store: Store = .{ .gpa = init.gpa };
    const address = try Io.net.IpAddress.parse("127.0.0.1", port);
    var server = try address.listen(io, .{ .reuse_address = true, .kernel_backlog = 2048 });
    var group: Io.Group = .init;
    defer group.cancel(io);
    while (true) {
        const stream = try server.accept(io);
        group.concurrent(io, serve, .{ io, &store, stream }) catch stream.close(io);
    }
}

fn serve(io: Io, store: *Store, stream: Io.net.Stream) void {
    defer stream.close(io);
    var recv: [8192]u8 = undefined;
    var send: [4096]u8 = undefined;
    var r = stream.reader(io, &recv);
    var w = stream.writer(io, &send);
    var server = std.http.Server.init(&r.interface, &w.interface);
    var arena: std.heap.ArenaAllocator = .init(store.gpa);
    defer arena.deinit();
    const window = store.gpa.alloc(u8, flate.max_window_len) catch return;
    defer store.gpa.free(window);
    while (server.reader.state == .ready) {
        _ = arena.reset(.retain_capacity);
        var request = server.receiveHead() catch return;
        handle(io, store, arena.allocator(), window, &request) catch return;
    }
}

/// One request: everything the handlers need from the head is copied
/// first, since reading the body invalidates it.
fn handle(io: Io, store: *Store, a: Allocator, window: []u8, request: *std.http.Server.Request) !void {
    const target = try a.dupe(u8, request.head.target);
    const method = request.head.method;
    var accept = false;
    var it = request.iterateHeaders();
    while (it.next()) |h| {
        if (std.ascii.eqlIgnoreCase(h.name, "accept-encoding")) accept = accept or acceptsGzip(h.value);
    }
    var body: []u8 = &.{};
    var rep: Reply = .{ .request = request, .a = a, .window = window, .accept = accept };
    if (method.requestHasBody()) {
        const n = request.head.content_length orelse 0;
        if (n > body_limit) return rep.problem(413, "the request is larger than the limit");
        body = try a.alloc(u8, @intCast(n));
        var buf: [256]u8 = undefined;
        const br = try request.readerExpectContinue(&buf);
        try br.readSliceAll(body);
    }
    const q = std.mem.indexOfScalar(u8, target, '?');
    const path = if (q) |k| target[0..k] else target;
    const query = if (q) |k| target[k + 1 ..] else "";
    if (std.mem.eql(u8, path, "/items")) {
        switch (method) {
            .POST => return create(io, store, &rep, body),
            .GET => return list(io, store, &rep, query),
            else => return rep.send(405, "", &.{.{ .name = "Allow", .value = "GET, HEAD, POST" }}),
        }
    }
    if (std.mem.startsWith(u8, path, "/items/") and std.mem.indexOfScalar(u8, path["/items/".len..], '/') == null) {
        const text = path["/items/".len..];
        switch (method) {
            .GET, .PUT, .DELETE => {},
            else => return rep.send(405, "", &.{.{ .name = "Allow", .value = "DELETE, GET, HEAD, PUT" }}),
        }
        const id = decimal(text) orelse return rep.problem(400, "an item id is a number");
        switch (method) {
            .GET => return get(io, store, &rep, id),
            .PUT => return put(io, store, &rep, id, body),
            else => return delete(io, store, &rep, id),
        }
    }
    return rep.send(404, "", &.{});
}

fn acceptsGzip(value: []const u8) bool {
    var parts = std.mem.splitScalar(u8, value, ',');
    while (parts.next()) |part| {
        const name = std.mem.trim(u8, part, " \t");
        const semi = std.mem.indexOfScalar(u8, name, ';') orelse name.len;
        if (std.mem.eql(u8, std.mem.trim(u8, name[0..semi], " \t"), "gzip")) return true;
    }
    return false;
}

const Reply = struct {
    request: *std.http.Server.Request,
    a: Allocator,
    window: []u8,
    accept: bool,

    /// Sends a body with its status and headers.
    fn send(rep: *Reply, status: u10, body: []const u8, headers: []const std.http.Header) !void {
        try rep.request.respond(body, .{ .status = @fromBackingInt(@intCast(status)), .extra_headers = headers });
    }

    /// Sends `value` as JSON with `status`, gzip-compressed when it is at
    /// least gzip_min bytes and the request accepts gzip, with `location`
    /// when it is not empty.
    fn json(rep: *Reply, status: u10, value: anytype, location: []const u8) !void {
        const text = try std.json.Stringify.valueAlloc(rep.a, value, .{});
        try rep.jsonText(status, text, location);
    }

    fn jsonText(rep: *Reply, status: u10, text: []const u8, location: []const u8) !void {
        var headers: [3]std.http.Header = undefined;
        var n: usize = 0;
        headers[n] = .{ .name = "Content-Type", .value = "application/json" };
        n += 1;
        var body = text;
        if (text.len >= gzip_min and rep.accept) {
            var out = try Io.Writer.Allocating.initCapacity(rep.a, 512);
            var c = try flate.Compress.init(&out.writer, rep.window, .gzip, .default);
            try c.writer.writeAll(text);
            try c.finish();
            body = out.written();
            headers[n] = .{ .name = "Content-Encoding", .value = "gzip" };
            n += 1;
        }
        if (location.len > 0) {
            headers[n] = .{ .name = "Location", .value = location };
            n += 1;
        }
        try rep.send(status, body, headers[0..n]);
    }

    fn problem(rep: *Reply, status: u10, message: []const u8) !void {
        try rep.json(status, Problem{ .message = message }, "");
    }
};

/// An optional + or - and then decimal digits, nothing else, in i64: what
/// Mk's str.ParseInt and Go's strconv accept. std.fmt.parseInt also takes
/// a '_' between digits, which decimal does not.
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

fn goodTag(tag: []const u8) bool {
    if (tag.len == 0 or tag.len > max_tag) return false;
    for (tag) |c| {
        if (!((c >= 'a' and c <= 'z') or (c >= '0' and c <= '9') or c == '-')) return false;
    }
    return true;
}

/// The request's item, or null after a 400 or 422 problem.
fn readItem(rep: *Reply, body: []const u8) !?ItemIn {
    if (!std.unicode.utf8ValidateSlice(body)) {
        try rep.problem(400, "the body is not UTF-8");
        return null;
    }
    // A repeated member of the same JSON type keeps its last value, as in
    // Mk's json.Parse and Go's decoder (std.json's default refuses it); a
    // repeat of another type is stated in the README. Numbers stay their
    // text (.parse_numbers = false), because std.json's Value makes "-0" a
    // float.
    const v = std.json.parseFromSliceLeaky(std.json.Value, rep.a, body, .{ .duplicate_field_behavior = .use_last, .parse_numbers = false }) catch {
        try rep.problem(400, "the body is not JSON");
        return null;
    };
    // The members by their JSON types, as Mk's codec and Go's decoder read
    // them: qty must be a JSON number written as an integer (decimal), name
    // and tag strings. std.json's parseFromValueLeaky would also turn 1.0,
    // 1e2 or "5" into the i64. Unknown members
    // are ignored.
    const fields: ?ItemIn = blk: {
        if (v != .object) break :blk null;
        const name = v.object.get("name") orelse break :blk null;
        const tag = v.object.get("tag") orelse break :blk null;
        const qty = v.object.get("qty") orelse break :blk null;
        if (name != .string or tag != .string or qty != .number_string) break :blk null;
        const q = decimal(qty.number_string) orelse break :blk null;
        break :blk .{ .name = name.string, .tag = tag.string, .qty = q };
    };
    const n = fields orelse {
        try rep.problem(422, "an item is an object with a string name and tag and an integer qty");
        return null;
    };
    if (n.name.len == 0 or n.name.len > max_name or !goodTag(n.tag) or n.qty < 0 or n.qty > max_qty) {
        try rep.problem(422, "an item needs a name of 1 to 64 bytes, a tag of 1 to 32 bytes of a-z, 0-9 and -, and a qty of 0 to 1000000000");
        return null;
    }
    return n;
}

fn create(io: Io, store: *Store, rep: *Reply, body: []const u8) !void {
    const n = (try readItem(rep, body)) orelse return;
    const id = blk: {
        try store.lock.lock(io);
        defer store.lock.unlock(io);
        const name = try store.gpa.dupe(u8, n.name);
        errdefer store.gpa.free(name);
        const tag = try store.gpa.dupe(u8, n.tag);
        errdefer store.gpa.free(tag);
        const id = store.next;
        try store.items.put(store.gpa, id, .{ .name = name, .tag = tag, .qty = n.qty });
        store.next += 1;
        break :blk id;
    };
    const location = try std.fmt.allocPrint(rep.a, "/items/{d}", .{id});
    try rep.json(201, Item{ .id = id, .name = n.name, .tag = n.tag, .qty = n.qty }, location);
}

fn get(io: Io, store: *Store, rep: *Reply, id: i64) !void {
    const text = blk: {
        try store.lock.lockShared(io);
        defer store.lock.unlockShared(io);
        const st = store.items.get(id) orelse break :blk null;
        break :blk try std.json.Stringify.valueAlloc(rep.a, Item{ .id = id, .name = st.name, .tag = st.tag, .qty = st.qty }, .{});
    };
    if (text) |t| return rep.jsonText(200, t, "");
    return rep.problem(404, "no such item");
}

fn put(io: Io, store: *Store, rep: *Reply, id: i64, body: []const u8) !void {
    const n = (try readItem(rep, body)) orelse return;
    const found = blk: {
        try store.lock.lock(io);
        defer store.lock.unlock(io);
        const st = store.items.getPtr(id) orelse break :blk false;
        const name = try store.gpa.dupe(u8, n.name);
        errdefer store.gpa.free(name);
        const tag = try store.gpa.dupe(u8, n.tag);
        store.gpa.free(st.name);
        store.gpa.free(st.tag);
        st.* = .{ .name = name, .tag = tag, .qty = n.qty };
        break :blk true;
    };
    if (!found) return rep.problem(404, "no such item");
    try rep.json(200, Item{ .id = id, .name = n.name, .tag = n.tag, .qty = n.qty }, "");
}

fn delete(io: Io, store: *Store, rep: *Reply, id: i64) !void {
    const found = blk: {
        try store.lock.lock(io);
        defer store.lock.unlock(io);
        const kv = store.items.fetchRemove(id) orelse break :blk false;
        store.gpa.free(kv.value.name);
        store.gpa.free(kv.value.tag);
        break :blk true;
    };
    if (!found) return rep.problem(404, "no such item");
    try rep.json(200, Deleted{ .deleted = true }, "");
}

/// The raw value of the first name=value pair of a query. The API's tags
/// and limits need no escapes, so a value is not decoded.
fn queryParam(query: []const u8, name: []const u8) ?[]const u8 {
    var pairs = std.mem.splitScalar(u8, query, '&');
    while (pairs.next()) |pair| {
        const eq = std.mem.indexOfScalar(u8, pair, '=') orelse pair.len;
        if (std.mem.eql(u8, pair[0..eq], name)) return if (eq < pair.len) pair[eq + 1 ..] else "";
    }
    return null;
}

fn byId(_: void, x: Item, y: Item) bool {
    return x.id < y.id;
}

fn list(io: Io, store: *Store, rep: *Reply, query: []const u8) !void {
    const tag = queryParam(query, "tag") orelse "";
    if (!goodTag(tag)) return rep.problem(400, "tag is 1 to 32 bytes of a-z, 0-9 and -");
    var limit: i64 = default_limit;
    if (queryParam(query, "limit")) |text| limit = decimal(text) orelse 0;
    if (limit < 1 or limit > max_limit) return rep.problem(400, "limit is 1 to 100");
    const text = blk: {
        try store.lock.lockShared(io);
        defer store.lock.unlockShared(io);
        var found: std.ArrayList(Item) = .empty;
        var each = store.items.iterator();
        while (each.next()) |e| {
            const st = e.value_ptr;
            if (std.mem.eql(u8, st.tag, tag)) try found.append(rep.a, .{ .id = e.key_ptr.*, .name = st.name, .tag = st.tag, .qty = st.qty });
        }
        std.mem.sortUnstable(Item, found.items, {}, byId);
        const total = found.items.len;
        const n = @min(total, @as(usize, @intCast(limit)));
        break :blk try std.json.Stringify.valueAlloc(rep.a, ItemList{ .items = found.items[0..n], .total = total }, .{});
    };
    try rep.jsonText(200, text, "");
}
