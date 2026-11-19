// ws-frames, Zig: std.websocket's receive path written once per language
// (README.md): decode a buffer of masked client frames into messages,
// fragments joined, each payload unmasked a byte at a time into one
// message buffer, each text message checked as UTF-8 by RFC 3629's table
// (a branch per lead byte), rounds passes. The buffer is batches copies of
// one batch of seven messages (binary 0, 125, 65,535 and 70,000 bytes; text
// 126 bytes, 30,000 bytes in three fragments split inside a character, and
// 1,000 bytes ending in a byte that is not UTF-8), each frame masked with
// the four low bytes of (f + 1) * 0x9E3779B97F4A7C15, f the frame's index
// in the buffer; binary payloads are splitmix64 bytes (seed 1), text the
// 10-byte unit "a", U+00E9, U+20AC, U+1F600 repeated. Per message the
// checksum adds its length, 7 when it is binary or valid text, and its
// byte at (r * 7919 + m) % length, m its index in the pass. Wrapping
// operators where the task wraps. Usage: ws-frames <batches> <rounds>.
const std = @import("std");

const Message = struct {
    op: u8,
    n: usize,
    frags: usize,
};

const batch = [7]Message{
    .{ .op = 2, .n = 0, .frags = 1 },
    .{ .op = 2, .n = 125, .frags = 1 },
    .{ .op = 1, .n = 126, .frags = 1 },
    .{ .op = 2, .n = 65535, .frags = 1 },
    .{ .op = 2, .n = 70000, .frags = 1 },
    .{ .op = 1, .n = 30000, .frags = 3 },
    .{ .op = 1, .n = 1000, .frags = 1 },
};

const unit = [10]u8{ 0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80 };

const max_msg: usize = 70000;

// build writes the frames of batches copies of the batch to out and
// returns their length.
fn build(out: []u8, pool: []u8, text: []u8, payload: []u8, batches: usize) usize {
    var st: u64 = 1;
    var i: usize = 0;
    while (i < max_msg) {
        st +%= 0x9E3779B97F4A7C15;
        var z = st;
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
        z ^= z >> 31;
        var k: u6 = 0;
        while (k < 8 and i < max_msg) : (k += 1) {
            pool[i] = @truncate(z >> (8 * k));
            i += 1;
        }
    }
    i = 0;
    while (i < max_msg) : (i += 1) {
        text[i] = unit[i % 10];
    }
    var o: usize = 0;
    var f: u64 = 0;
    var b: usize = 0;
    while (b < batches) : (b += 1) {
        for (batch) |m| {
            const src = if (m.op == 1) text else pool;
            i = 0;
            while (i < m.n) : (i += 1) {
                payload[i] = src[i];
            }
            if (m.n == 1000) payload[999] = 0xFF;
            // Fragments: the first n/3 - 1 bytes, the next n/3 + 1, the rest.
            var at: usize = 0;
            var j: usize = 0;
            while (j < m.frags) : (j += 1) {
                var l = m.n - at;
                if (m.frags == 3 and j == 0) {
                    l = m.n / 3 - 1;
                } else if (m.frags == 3 and j == 1) {
                    l = m.n / 3 + 1;
                }
                var b0: u8 = 0;
                if (j == 0) b0 = m.op;
                if (j == m.frags - 1) b0 |= 0x80;
                out[o] = b0;
                o += 1;
                if (l <= 125) {
                    out[o] = 0x80 | @as(u8, @intCast(l));
                    o += 1;
                } else if (l <= 65535) {
                    out[o] = 0x80 | 126;
                    out[o + 1] = @truncate(l >> 8);
                    out[o + 2] = @truncate(l);
                    o += 3;
                } else {
                    out[o] = 0x80 | 127;
                    out[o + 1] = 0;
                    out[o + 2] = 0;
                    out[o + 3] = 0;
                    out[o + 4] = 0;
                    out[o + 5] = @truncate(l >> 24);
                    out[o + 6] = @truncate(l >> 16);
                    out[o + 7] = @truncate(l >> 8);
                    out[o + 8] = @truncate(l);
                    o += 9;
                }
                const kw: u64 = (f +% 1) *% 0x9E3779B97F4A7C15;
                const key = [4]u8{ @truncate(kw), @truncate(kw >> 8), @truncate(kw >> 16), @truncate(kw >> 24) };
                var k: usize = 0;
                while (k < 4) : (k += 1) {
                    out[o + k] = key[k];
                }
                o += 4;
                i = 0;
                while (i < l) : (i += 1) {
                    out[o + i] = payload[at + i] ^ key[i & 3];
                }
                o += l;
                at += l;
                f += 1;
            }
        }
    }
    return o;
}

// validUtf8 reports whether s is UTF-8 by RFC 3629's table of well-formed
// sequences: a branch per lead byte, the second byte's range by the lead,
// the rest continuation bytes.
fn validUtf8(s: []const u8) bool {
    const n = s.len;
    var i: usize = 0;
    while (i < n) {
        const c = s[i];
        if (c < 0x80) {
            i += 1;
            continue;
        }
        var need: usize = 0;
        var lo: u8 = 0x80;
        var hi: u8 = 0xBF;
        if (c < 0xC2) {
            return false;
        } else if (c < 0xE0) {
            need = 1;
        } else if (c < 0xF0) {
            need = 2;
            if (c == 0xE0) {
                lo = 0xA0;
            } else if (c == 0xED) {
                hi = 0x9F;
            }
        } else if (c < 0xF5) {
            need = 3;
            if (c == 0xF0) {
                lo = 0x90;
            } else if (c == 0xF4) {
                hi = 0x8F;
            }
        } else {
            return false;
        }
        if (i + need >= n) return false;
        const b = s[i + 1];
        if (b < lo or b > hi) return false;
        var k: usize = 2;
        while (k <= need) : (k += 1) {
            if (s[i + k] & 0xC0 != 0x80) return false;
        }
        i += need + 1;
    }
    return true;
}

// decode is one pass over buf: the checksum it adds for round r.
fn decode(buf: []const u8, msg: []u8, r: u64) u64 {
    const len = buf.len;
    var sum: u64 = 0;
    var pos: usize = 0;
    var mlen: usize = 0;
    var mop: u8 = 0;
    var count: u64 = 0;
    while (pos + 2 <= len) {
        const b0 = buf[pos];
        const b1 = buf[pos + 1];
        var l: usize = b1 & 0x7F;
        var hl: usize = 2;
        if (l == 126) {
            if (pos + 4 > len) return sum +% 1000003;
            l = @as(usize, buf[pos + 2]) << 8 | buf[pos + 3];
            hl = 4;
        } else if (l == 127) {
            if (pos + 10 > len) return sum +% 1000003;
            var x: u64 = 0;
            var k: usize = 2;
            while (k < 10) : (k += 1) {
                x = x << 8 | buf[pos + k];
            }
            if (x > max_msg) return sum +% 1000003;
            l = @intCast(x);
            hl = 10;
        }
        const op = b0 & 0x0F;
        if (b0 & 0x70 != 0 or b1 & 0x80 == 0 or l > len - pos - hl - 4) return sum +% 1000003;
        if (op != 0) {
            mop = op;
            mlen = 0;
        }
        if (l > max_msg - mlen) return sum +% 1000003;
        const k = pos + hl;
        const key = [4]u8{ buf[k], buf[k + 1], buf[k + 2], buf[k + 3] };
        pos = k + 4;
        var i: usize = 0;
        while (i < l) : (i += 1) {
            msg[mlen + i] = buf[pos + i] ^ key[i & 3];
        }
        mlen += l;
        pos += l;
        if (b0 & 0x80 != 0) {
            sum +%= mlen;
            if (mop != 1 or validUtf8(msg[0..mlen])) sum +%= 7;
            if (mlen > 0) sum +%= msg[@intCast((r *% 7919 +% count) % mlen)];
            count += 1;
        }
    }
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const batches = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const pool = try pa.alloc(u8, max_msg);
    defer pa.free(pool);
    const text = try pa.alloc(u8, max_msg);
    defer pa.free(text);
    const payload = try pa.alloc(u8, max_msg);
    defer pa.free(payload);
    const out = try pa.alloc(u8, batches * (166786 + 9 * 14));
    defer pa.free(out);
    const len = build(out, pool, text, payload, batches);
    const msg = try pa.alloc(u8, max_msg);
    defer pa.free(msg);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        total +%= decode(out[0..len], msg, r);
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
