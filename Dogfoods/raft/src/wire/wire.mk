// Why: messages cross real UDP sockets, which may corrupt them.
// What: Encode, Decode, In, Out and the errors Short, BadMagic, BadChecksum,
// BadCount and BadField.
// How: a versioned header with a magic and a checksum, then fixed-size
// fields; Decode checks every count and field before trusting it.

import std.hash
import src.raft

/// A datagram shorter than its header and fields say it must be.
export error Short(len: int)
/// A datagram that does not start with "RF" and version 1.
export error BadMagic
/// A kind, operation or endpoint byte outside the protocol's range.
export error BadField(offset: int, value: int)
/// A batch longer than raft.MaxBatch.
export error BadCount(count: int)
/// A datagram whose CRC-32 does not match its bytes.
export error BadChecksum
/// Everything Decode can refuse.
export type Error = Short | BadMagic | BadField | BadCount | BadChecksum

/// The bytes a buffer needs to hold any encoded message.
export const MaxSize: int = 512

// The layout, little-endian: "RF", version, kind, from, to, count, flags
// (bit 0 ok, bit 1 served), leader; eight i64 fields; the command; count
// entries of a term and a command; a CRC-32 of everything before it.
const Header: int = 9
const CommandSize: int = 30
const Fixed: int = Header + 8 * 8 + CommandSize
const EntrySize: int = 8 + CommandSize
const Version: u8 = 1

struct Out {
    buf: mut []u8
    n: int
}

Out.U8(mut self, x: u8) {
    self.buf[self.n] = x
    self.n += 1
}

Out.I64(mut self, x: i64) {
    for i in 0..8 {
        self.U8(Trunc[u8](x >> (8 * i)))
    }
}

Out.Command(mut self, c: &raft.Command) {
    for i in 0..4 {
        self.U8(Trunc[u8](c.client >> (8 * i)))
    }
    self.I64(c.seq)
    self.U8(u8(c.op))
    self.U8(c.key)
    self.I64(c.arg)
    self.I64(c.arg2)
}

struct In {
    buf: []u8
    n: int
}

In.U8(mut self): u8 {
    b := self.buf[self.n]
    self.n += 1
    return b
}

In.I64(mut self): i64 {
    var x: u64 = 0
    for i in 0..8 {
        x = x | (u64(self.U8()) << (8 * i))
    }
    return Trunc[i64](x)
}

In.Command(mut self): BadField!raft.Command {
    var client: u32 = 0
    for i in 0..4 {
        client = client | (u32(self.U8()) << (8 * i))
    }
    seq := self.I64()
    at := self.n
    code := self.U8()
    op := match code {
        0 { raft.Op.Noop }
        1 { raft.Op.Get }
        2 { raft.Op.Put }
        3 { raft.Op.Cas }
        _ { fail BadField(offset: at, value: int(code)) }
    }
    key := self.U8()
    if int(key) >= raft.MaxKeys {
        fail BadField(offset: at + 1, value: int(key))
    }
    arg := self.I64()
    arg2 := self.I64()
    return raft.Command(client: Trunc[i32](client), seq: seq, op: op, key: key, arg: arg, arg2: arg2)
}

/// The bytes one log entry takes on disk.
export const EntryBytes: int = EntrySize

/// Writes e into buf in the layout of an entry inside a message. O(1).
export EncodeEntry(e: &raft.Entry, buf: mut []u8)
    requires buf.Len() >= EntryBytes
{
    var w := Out(buf: buf, n: 0)
    w.I64(e.term)
    w.Command(&e.cmd)
}

/// Reads an entry EncodeEntry wrote. Fails with Short when data is cut and
/// BadField for an operation or key out of range. O(1).
export DecodeEntry(data: []u8): Short|BadField!raft.Entry {
    if data.Len() < EntryBytes {
        fail Short(len: data.Len())
    }
    var r := In(buf: data, n: 0)
    term := r.I64()
    cmd := try r.Command()
    return raft.Entry(term: term, cmd: cmd)
}

/// Writes m into buf and returns its length, at most MaxSize. O(size);
/// allocates nothing.
export Encode(m: &raft.Msg, buf: mut []u8): int
    requires buf.Len() >= MaxSize
{
    var w := Out(buf: buf, n: 0)
    w.U8(b'R')
    w.U8(b'F')
    w.U8(Version)
    w.U8(u8(m.kind))
    w.U8(m.from)
    w.U8(m.to)
    w.U8(m.count)
    ok: u8 = if m.ok { 1 } else { 0 }
    served: u8 = if m.served { 2 } else { 0 }
    w.U8(ok | served)
    w.U8(Trunc[u8](m.leader))
    w.I64(m.term)
    w.I64(m.last_index)
    w.I64(m.last_term)
    w.I64(m.prev_index)
    w.I64(m.prev_term)
    w.I64(m.commit)
    w.I64(m.match_index)
    w.I64(m.value)
    w.Command(&m.cmd)
    for i in 0..int(m.count) {
        w.I64(m.entries[i].term)
        w.Command(&m.entries[i].cmd)
    }
    sum := hash.Crc32(w.buf[..w.n])
    for i in 0..4 {
        w.U8(Trunc[u8](sum >> (8 * i)))
    }
    return w.n
}

/// Reads one message from a whole datagram. Fails with Short when data is
/// cut, BadMagic for another protocol, BadField or BadCount for a value out
/// of range and BadChecksum when a byte changed in flight. O(len);
/// allocates nothing.
export Decode(data: []u8): Error!raft.Msg {
    if data.Len() < Fixed + 4 {
        fail Short(len: data.Len())
    }
    if data[0] != b'R' || data[1] != b'F' || data[2] != Version {
        fail BadMagic
    }
    count := int(data[6])
    if count > raft.MaxBatch {
        fail BadCount(count: count)
    }
    size := Fixed + count * EntrySize
    if data.Len() != size + 4 {
        fail Short(len: data.Len())
    }
    var sum: u32 = 0
    for i in 0..4 {
        sum = sum | (u32(data[size + i]) << (8 * i))
    }
    if sum != hash.Crc32(data[..size]) {
        fail BadChecksum
    }
    var r := In(buf: data, n: 3)
    kind := match r.U8() {
        1 { raft.Kind.Vote }
        2 { raft.Kind.VoteReply }
        3 { raft.Kind.Append }
        4 { raft.Kind.AppendReply }
        5 { raft.Kind.Request }
        6 { raft.Kind.Reply }
        _ { fail BadField(offset: 3, value: int(data[3])) }
    }
    from := r.U8()
    to := r.U8()
    for i in 4..6 {
        if int(data[i]) >= raft.MaxNodes + raft.MaxClients {
            fail BadField(offset: i, value: int(data[i]))
        }
    }
    _ = r.U8()
    flags := r.U8()
    var m := raft.Msg.New(kind, int(from), int(to), 0)
    m.count = Trunc[u8](count)
    m.ok = flags & 1 != 0
    m.served = flags & 2 != 0
    m.leader = Trunc[i8](r.U8())
    m.term = r.I64()
    m.last_index = r.I64()
    m.last_term = r.I64()
    m.prev_index = r.I64()
    m.prev_term = r.I64()
    m.commit = r.I64()
    m.match_index = r.I64()
    m.value = r.I64()
    m.cmd = try r.Command()
    for i in 0..count {
        term := r.I64()
        cmd := try r.Command()
        m.entries[i] = raft.Entry(term: term, cmd: cmd)
    }
    return m
}
