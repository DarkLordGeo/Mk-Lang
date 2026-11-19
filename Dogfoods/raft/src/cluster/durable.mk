// Why: a Raft node must find its term, vote and log again after a crash.
// What: Durable, Save, Load and the BadState error.
// How: a header line, then the entries in the wire format; Save writes a
// new file, syncs it and renames it over the old, so a crash never mixes them.

import std.fs
import src.raft
import src.wire

/// A node state file that is not one: its header or an entry does not read.
export error BadState(node: int)

// What a node made durable: its term, its vote and its log.
struct Durable {
    term: i64
    vote: int
    entries: List[raft.Entry]
}

// node<i>.raft holds a header line, "raft-node 1 term=T vote=V entries=N",
// then N entries in the wire format. Save writes node<i>.new, syncs it and
// renames it over node<i>.raft, so a crash leaves the old state or the new
// one and never a mix.
Save(a: &Arena, io: Io, i: int, n: &raft.Node): fs.Error|OutOfMemory!void {
    var scratch := try a.Sub()
    defer scratch.Free()
    entries := n.Entries()
    header := try scratch.Fmt("raft-node 1 term={t} vote={v} entries={c}\n", (t: n.CurrentTerm(), v: n.Vote(), c: entries.Len()))
    body := try scratch.Slice[u8](entries.Len() * wire.EntryBytes)
    for k, e in entries {
        wire.EncodeEntry(&e, mut body[k * wire.EntryBytes..])
    }
    fresh := try scratch.Fmt("node{i}.new", (i))
    path := try scratch.Fmt("node{i}.raft", (i))
    var f := try fs.Create(io, fresh)
    onerr f.Abandon()
    try f.WriteAll(io, header.AsBytes())
    try f.WriteAll(io, body[..])
    try f.Sync(io)
    try f.Close(io)
    try fs.Rename(io, fresh, path)
}

// The state node i saved last, or none when it never saved one. Fails as
// fs.ReadBytes does, and with BadState for a file Save did not write.
Load(a: &Arena, io: Io, i: int): fs.Error|OutOfMemory|BadState!?Durable from a {
    path := try a.Fmt("node{i}.raft", (i))
    data := fs.ReadBytes(a, io, path) else e {
        match e {
            fs.NotFound {
                return none
            }
            _ {
                fail e
            }
        }
    }
    end := data.Index(&b'\n') else {
        fail BadState(node: i)
    }
    line := str.From(data[..end]) else {
        fail BadState(node: i)
    }
    var words: [5]str = [""; 5]
    var n := 0
    for word in line.Words() {
        if n == 5 {
            fail BadState(node: i)
        }
        words[n] = word
        n += 1
    }
    if n != 5 || words[0] != "raft-node" || words[1] != "1" {
        fail BadState(node: i)
    }
    term := try Field(words[2], "term=", i)
    vote := try Field(words[3], "vote=", i)
    count := try Field(words[4], "entries=", i)
    body := data[end + 1..]
    if count < 0 || body.Len() != Trunc[int](count) * wire.EntryBytes {
        fail BadState(node: i)
    }
    var entries := List[raft.Entry].New(a)
    for k in 0..Trunc[int](count) {
        e := wire.DecodeEntry(body[k * wire.EntryBytes..]) else {
            fail BadState(node: i)
        }
        try entries.Push(e)
    }
    return some(Durable(term: term, vote: Trunc[int](vote), entries: entries))
}

Field(word: str, key: str, node: int): BadState!i64 {
    if !word.StartsWith(key) {
        fail BadState(node: node)
    }
    return word[key.Len()..].ParseInt() else {
        fail BadState(node: node)
    }
}
