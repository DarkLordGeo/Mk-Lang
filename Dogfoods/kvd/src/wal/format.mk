// Why: the log's on-disk format, kept apart from the code that writes it.
// What: Header, ReadHeader, Checksum, RecordStart, RecordSize and Records,
// which iterates a log's records and says how it ended.
// How: length-prefixed, checksummed records after a fixed header, as below.

// The write-ahead log's file format. Every integer is little-endian.
//
//   header, 24 bytes   "KVDWAL1\n", generation u64, CRC-32 of the first 16
//                      bytes u32, clean-shutdown flag u8, three zero bytes
//   record             payload length u32, CRC-32 of the payload u32, payload
//   payload            operation u8, key length u16, key, value
//
// A record is written whole and then synced, so after a crash the file is a
// run of good records followed by at most one torn one. A bad record with
// good bytes after it is corruption, never a torn write.
import std.hash
import src.le
import src.store

/// The size of the file header, in bytes.
export const HeaderSize: int = 24

/// The size of a record's length and checksum, in bytes.
export const RecordHead: int = 8

/// The size of the payload's fixed part: operation and key length.
export const PayloadHead: int = 3

/// The offset of the clean-shutdown flag in the header.
export const CleanOffset: int = 20

/// The first eight bytes of every log.
export const Magic: str = "KVDWAL1\n"

/// The header of a log of generation `generation`.
export Header(generation: u64): [24]u8 {
    var h: [24]u8 = [0; 24]
    for i, c in Magic.AsBytes() {
        h[i] = c
    }
    le.PutU64(mut h[..], 8, generation)
    le.PutU32(mut h[..], 16, hash.Crc32(h[..16]))
    return h
}

/// What a header says, once its magic and checksum hold.
export struct HeaderInfo {
    export generation: u64
    export clean: bool
}

/// Reads a header from the first `HeaderSize` bytes of `b`.
export ReadHeader(b: []u8): ?HeaderInfo {
    if b.Len() < HeaderSize {
        return none
    }
    magic := str.From(b[..8]) else {
        return none
    }
    if magic != Magic {
        return none
    }
    if le.U32(b, 16) != hash.Crc32(b[..16]) {
        return none
    }
    return some(HeaderInfo(generation: le.U64(b, 8), clean: b[CleanOffset] == 1))
}

/// The CRC-32 of a payload, fed in its three pieces.
export Checksum(head: []u8, key: str, value: str): u32 {
    var c := hash.Crc32State.New()
    c.Bytes(head)
    c.Bytes(key.AsBytes())
    c.Bytes(value.AsBytes())
    return c.Sum()
}

/// The first eleven bytes of a record: length, checksum, operation and key
/// length. The key and the value follow them.
export RecordStart(op: store.Op, key: str, value: str): [11]u8 {
    var b: [11]u8 = [0; 11]
    b[8] = u8(op)
    le.PutU16(mut b[..], 9, Cast[u16](key.Len()))
    le.PutU32(mut b[..], 0, Cast[u32](PayloadHead + key.Len() + value.Len()))
    le.PutU32(mut b[..], 4, Checksum(b[8..], key, value))
    return b
}

/// The size of a record on disk.
export RecordSize(key: str, value: str): int {
    return RecordHead + PayloadHead + key.Len() + value.Len()
}

/// One record read back, with views of the log's bytes.
export struct Record {
    export offset: i64
    export op: store.Op
    export key: str
    export value: str
}

/// How a scan of the records ended.
export enum End {
    /// Every byte was a good record.
    Clean
    /// The last record was cut short by a crash: `bytes` bytes from `offset`.
    Torn(offset: i64, bytes: i64)
    /// A bad record with more bytes after it, which no crash can leave.
    Corrupt(offset: i64)
}

/// Reads records one at a time from a log's bytes after the header.
export view struct Records {
    data: []u8
    pos: int
    stop: End
}

/// Starts reading `data`, a whole log, at its first record.
export Records.New(data: []u8): Records {
    return Records(data: data, pos: HeaderSize, stop: End.Clean)
}

/// How the scan ended; `Clean` until `Next` has returned `none`.
export Records.End(&self): End {
    return self.stop
}

/// The offset of the first byte not yet read: after a scan, the length of
/// the good part of the log.
export Records.Offset(&self): i64 {
    return i64(self.pos)
}

/// The next good record, or `none` at the end or at the first bad one, after
/// which `End` says which. O(record); allocates nothing.
export Records.Next(mut self): ?Record from self.data {
    left := self.data.Len() - self.pos
    if left == 0 {
        return none
    }
    at := i64(self.pos)
    if left < RecordHead + PayloadHead {
        self.stop = End.Torn(offset: at, bytes: i64(left))
        return none
    }
    n := Cast[int](le.U32(self.data, self.pos))
    sum := le.U32(self.data, self.pos + 4)
    body := self.pos + RecordHead
    if n < PayloadHead || n > left - RecordHead {
        self.stop = End.Torn(offset: at, bytes: i64(left))
        return none
    }
    payload := self.data[body..body + n]
    rec := Decode(at, payload, sum) else {
        // a bad record that ends the file is the torn last write; one with
        // bytes after it is damage
        self.stop = if body + n == self.data.Len() {
            End.Torn(offset: at, bytes: i64(left))
        } else {
            End.Corrupt(offset: at)
        }
        return none
    }
    self.pos = body + n
    return some(rec)
}

/// Decodes a payload whose stored checksum is `sum`, or `none` when the
/// checksum, the operation, the key length or the text does not hold.
Decode(at: i64, payload: []u8, sum: u32): ?Record from payload {
    if hash.Crc32(payload) != sum {
        return none
    }
    op := store.Op.FromByte(payload[0]) else {
        return none
    }
    klen := int(le.U16(payload, 1))
    if klen > payload.Len() - PayloadHead {
        return none
    }
    key := str.From(payload[PayloadHead..PayloadHead + klen]) else {
        return none
    }
    value := str.From(payload[PayloadHead + klen..]) else {
        return none
    }
    return some(Record(offset: at, op: op, key: key, value: value))
}

impl Format for End {
    Format(&self, w: mut Writer): WriteError!void {
        match *self {
            .Clean {
                try w.Str("clean")
            }
            .Torn(offset, bytes) {
                try Print(w, "torn record at {offset} ({bytes} bytes dropped)", (offset, bytes))
            }
            .Corrupt(offset) {
                try Print(w, "corrupt record at {offset}", (offset))
            }
        }
    }
}
