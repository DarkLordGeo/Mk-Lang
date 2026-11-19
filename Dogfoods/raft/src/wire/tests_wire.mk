// Why: every message must survive encoding, and damaged datagrams must fail.
// What: round-trip and refusal tests of Encode and Decode.
// How: Sample builds messages; Refused checks the error of a broken one.

import src.raft

Sample(): raft.Msg {
    var m := raft.Msg.New(raft.Kind.Append, 2, 4, 7)
    m.prev_index = 11
    m.prev_term = 6
    m.commit = 10
    m.count = 2
    m.entries[0] = raft.Entry(term: 6, cmd: raft.Command(client: 1, seq: 9, op: raft.Op.Put, key: 3, arg: -5, arg2: 0))
    m.entries[1] = raft.Entry(term: 7, cmd: raft.Command(client: 2, seq: 4, op: raft.Op.Cas, key: 1, arg: 1, arg2: 9_000_000_000))
    m.ok = true
    m.leader = -1
    m.value = -42
    return m
}

Refused(data: []u8): bool {
    _ = Decode(data) else {
        return true
    }
    return false
}

test "Decode gives back every field Encode wrote" {
    m := Sample()
    var buf: [MaxSize]u8 = [0; MaxSize]
    n := Encode(&m, mut buf[..])
    back := try Decode(buf[..n])
    Assert(back.kind == raft.Kind.Append && back.from == 2 && back.to == 4 && back.term == 7)
    Assert(back.prev_index == 11 && back.prev_term == 6 && back.commit == 10 && back.count == 2)
    Assert(back.ok && !back.served && back.leader == -1 && back.value == -42)
    Assert(back.entries[0].cmd.arg == -5 && back.entries[1].cmd.arg2 == 9_000_000_000)
    Assert(back.entries[1].cmd.op == raft.Op.Cas && back.entries[1].term == 7)
}

test "Decode refuses a datagram with one byte changed" {
    m := Sample()
    var buf: [MaxSize]u8 = [0; MaxSize]
    n := Encode(&m, mut buf[..])
    buf[20] = buf[20] ^ 1
    var checksum := false
    _ = Decode(buf[..n]) else e {
        match e {
            BadChecksum {
                checksum = true
            }
            _ {
            }
        }
    }
    Assert(checksum)
}

test "Decode refuses a cut datagram and another protocol" {
    m := Sample()
    var buf: [MaxSize]u8 = [0; MaxSize]
    n := Encode(&m, mut buf[..])
    Assert(Refused(buf[..n - 1]))
    Assert(Refused(buf[..3]))
    buf[0] = b'X'
    Assert(Refused(buf[..n]))
}
