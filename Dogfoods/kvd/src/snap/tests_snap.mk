// Why: a snapshot is read by binary search over mapped bytes.
// What: tests of View.Find, the trailer's checksum and the magic check.
// How: builds a two-entry snapshot in a buffer and reads it back.

import std.hash
import src.le

/// A snapshot of generation 4 holding `a = 1` and `b = 22`, in `out`;
/// returns its length.
TwoEntries(out: mut []u8): int {
    for i, c in Magic.AsBytes() {
        out[i] = c
    }
    le.PutU64(out, 8, 4)
    le.PutU64(out, 16, 0)
    le.PutU64(out, 24, 2)
    le.PutU32(out, 32, hash.Crc32(out[..32]))
    first := HeaderSize + 16
    second := first + EntryHead + 2
    le.PutU64(out, HeaderSize, Cast[u64](first))
    le.PutU64(out, HeaderSize + 8, Cast[u64](second))
    le.PutU16(out, first, 1)
    le.PutU32(out, first + 2, 1)
    out[first + EntryHead] = b'a'
    out[first + EntryHead + 1] = b'1'
    le.PutU16(out, second, 1)
    le.PutU32(out, second + 2, 2)
    out[second + EntryHead] = b'b'
    out[second + EntryHead + 1] = b'2'
    out[second + EntryHead + 2] = b'2'
    end := second + EntryHead + 3
    le.PutU32(out, end, hash.Crc32(out[HeaderSize..end]))
    return end + 4
}

test "a snapshot's keys are found by binary search, and a missing one is not" {
    var buf: [128]u8 = [0; 128]
    n := TwoEntries(mut buf[..])
    v := Check(buf[..n]).Must()
    Assert(v.Len() == 2)
    Assert(v.Info().generation == 4)
    Assert(v.Find("a").Must() == "1")
    Assert(v.Find("b").Must() == "22")
    Assert(v.Find("c").IsNone())
}

test "a flipped byte in the entries fails the trailer's checksum" {
    var buf: [128]u8 = [0; 128]
    n := TwoEntries(mut buf[..])
    buf[n - 6] = b'3'
    _ = Check(buf[..n]) else e {
        match e {
            Damaged(offset) {
                Assert(offset == i64(n - 4))
            }
            _ {
                Assert(false)
            }
        }
        return
    }
    Assert(false)
}

test "bytes that are no snapshot are refused before anything is read" {
    var buf: [128]u8 = [0; 128]
    _ = Check(buf[..60]) else e {
        match e {
            NotSnapshot {}
            _ {
                Assert(false)
            }
        }
        return
    }
    Assert(false)
}
