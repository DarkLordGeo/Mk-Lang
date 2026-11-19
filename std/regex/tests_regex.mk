// Why: what the compiled program, the DFAs and the backtracker hold is not
// visible through any exported call, but must still be tested.
// What: test blocks of std.regex's internals, run by `mk test
// --std=std.regex` (TestSTDREGEX_TestBlocks), with the helpers named Tr*.
// How: helper names start with Tr, since every file of std/regex is one
// module and the names must not collide.

import std.core

TrBoth(a: &Arena, p: str): bool {
    re := Compile(a, p) else {
        return false
    }
    return re.dfa.ok && re.rdfa.ok
}

TrRevInsts(a: &Arena, p: str): int {
    t := ParseTree(a, p) else {
        return -1
    }
    q := CompileTree(a, t, true) else {
        return -1
    }
    return q.insts.Len()
}

test "patterns of letter runs build both DFAs within the state budget" (sys: &System) {
    Assert(TrBoth(sys.arena, "\\pL+"))
    Assert(TrBoth(sys.arena, "\\w+\\s\\pL"))
    Assert(TrBoth(sys.arena, "[\\pL\\pN_]+"))
    Assert(TrBoth(sys.arena, "(?i)\\pL+"))
}

test "a reversed Unicode class is a small DAG and keeps the reverse DFA" (sys: &System) {
    Assert(TrBoth(sys.arena, "\\pL"))
    Assert(TrBoth(sys.arena, "(?i)\\pL"))
    Assert(TrBoth(sys.arena, "\\p{Lu}\\pL*"))
    Assert(TrBoth(sys.arena, "\\p{Greek}+x"))
    // The forward program of \pL has 308 instructions; as a Split chain of
    // byte chains the reversed one would have about 3,700.
    n := TrRevInsts(sys.arena, "\\pL")
    Assert(n > 0 && n <= 600)
    Assert(TrRevInsts(sys.arena, ".") <= 16)
}

// TrScalar is the UTF-8 text of scalar c in buf.
TrScalar(buf: mut [4]u8, c: u32): int {
    e := Encode(c)
    for k in 0..e.n {
        buf[k] = e.b[k]
    }
    return e.n
}

test "the reversed class of a table takes exactly its scalars" (sys: &System) {
    re := Compile(sys.arena, "\\pL") else {
        Assert(false)
        return
    }
    up := Compile(sys.arena, "\\p{Lu}") else {
        Assert(false)
        return
    }
    Assert(re.rdfa.ok && up.rdfa.ok)
    var buf: [4]u8 = [0; 4]
    var c: u32 = 0
    for c <= MaxRune {
        if c == 0xD800 {
            c = 0xE000
        }
        n := TrScalar(mut buf, c)
        b := buf[0..n]
        // The reverse DFA alone, from the end of the scalar back to 0.
        Assert((ScanRev(&re.rdfa, b, 0, n) == 0) == InUcd(UcdLetter, c))
        Assert((ScanRev(&up.rdfa, b, 0, n) == 0) == InUcd(UcdFind("Lu".AsBytes()), c))
        c += 1
    }
}

test "Group runs the backtracker on a short match of a large program" (sys: &System) {
    re := Compile(sys.arena, "([a-z]{1,600})") else {
        Assert(false)
        return
    }
    Assert(re.insts.Len() > 1000)
    s := "hello world"
    b := Back(&re, s.AsBytes(), 0, 5, 1)
    Assert(b.state == BtFound && b.hi == 5 && b.g0 == 0 && b.g1 == 5)
    m := re.Find(s).Must()
    Assert(m.lo == 0 && m.hi == 5)
    Assert(re.Group(s, m, 1).Must() == "hello")
    // FindAll's second match, at 6.
    Assert(re.Group(s, Match(lo: 6, hi: 11), 1).Must() == "world")
}

test "a thread that would read past the match gives the run to the Pike VM" (sys: &System) {
    re := Compile(sys.arena, "(a)(?:bc)?") else {
        Assert(false)
        return
    }
    s := "abd"
    // The higher-priority thread takes the b at hi = 1 before it dies.
    Assert(Back(&re, s.AsBytes(), 0, 1, 1).state == BtGiveUp)
    m := re.Find(s).Must()
    Assert(m.lo == 0 && m.hi == 1)
    Assert(re.Group(s, m, 1).Must() == "a")
    // An m that is not a match: the backtracker's match ends elsewhere.
    Assert(re.Group("ab", Match(lo: 0, hi: 2), 1).IsNone())
}

test "a full job stack gives the run to the Pike VM" (sys: &System) {
    // Each pass of the star leaves three jobs (the Split's other branch
    // and the two slot restores), so 30 passes overflow the smallest
    // class's 64 jobs, and the answer is still the Pike VM's.
    re := Compile(sys.arena, "(a)*") else {
        Assert(false)
        return
    }
    s := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    Assert(re.insts.Len() * 31 <= 1024)
    Assert(Back(&re, s.AsBytes(), 0, 30, 1).state == BtGiveUp)
    m := re.Find(s).Must()
    Assert(m.lo == 0 && m.hi == 30)
    g := re.Span(s.AsBytes(), m, 1).Must()
    Assert(g.lo == 29 && g.hi == 30)
    // Ten passes, on a text of ten bytes, fit.
    t := s.AsBytes()[0..10]
    b := Back(&re, t, 0, 10, 1)
    Assert(b.state == BtFound && b.g0 == 9 && b.g1 == 10)
}

test "a reference name reads letters and digits of every script" (sys: &System) {
    Assert(NameScalar("é".AsBytes(), 0) == 2)
    Assert(NameScalar("\u{661}".AsBytes(), 0) == 2)
    Assert(NameScalar("日".AsBytes(), 0) == 3)
    Assert(NameScalar("_".AsBytes(), 0) == 1)
    Assert(NameScalar("€".AsBytes(), 0) == 0)
    Assert(NameScalar("-".AsBytes(), 0) == 0)
    Assert(NameScalar("\u{2160}".AsBytes(), 0) == 0)
    re := Compile(sys.arena, "(?P<x>a)") else {
        Assert(false)
        return
    }
    a := sys.arena
    Assert(re.Replace(a, "a", "$1é").Must() == "")
    Assert(re.Replace(a, "a", "$xé").Must() == "")
    Assert(re.Replace(a, "a", "$x\u{661}").Must() == "")
    Assert(re.Replace(a, "a", "${x}é").Must() == "aé")
    Assert(re.Replace(a, "a", "${1}é").Must() == "aé")
    Assert(re.Replace(a, "a", "$x€").Must() == "a€")
}

// TrWord is the text of n letters a and b, bit k of x choosing b.
TrWord(buf: mut [24]u8, x: u32, n: int): str {
    for k in 0..n {
        buf[k] = if (x >> Trunc[u32](k)) & 1 == 1 { b'b' } else { b'a' }
    }
    return str.From(buf[0..n]).Must()
}

test "a DFA of 2,050 states built in Compile's freed sub-arena finds what the Pike VM finds" (sys: &System) {
    // The forward DFA of [ab]*a[ab]{9} remembers the last 10 letters.
    p := "[ab]*a[ab]{9}"
    re := Compile(sys.arena, p) else {
        Assert(false)
        return
    }
    vm := CompileIn(sys.arena, p, 2) else {
        Assert(false)
        return
    }
    Assert(re.dfa.ok && re.rdfa.ok)
    Assert(re.dfa.trans.Len() % Cast[int](re.dfa.stride) == 0)
    Assert(re.dfa.trans.Len() / Cast[int](re.dfa.stride) >= 2048)
    // Past its budget a DFA keeps no table: matching never scans it.
    Assert(!vm.dfa.ok && vm.dfa.trans.Len() == 0 && vm.rdfa.trans.Len() == 0)
    // Fill new heap memory, which may be the freed sub-arena's, so a table
    // left there could read as junk.
    var junk := sys.arena.Sub() else {
        Assert(false)
        return
    }
    defer junk.Free()
    fill := junk.Slice[u32](1 << 20) else {
        Assert(false)
        return
    }
    fill.Fill(0xFFFFFFFF)
    var buf: [24]u8 = [0; 24]
    var x: u32 = 0x9E3779B9
    for n in 0..24 {
        for _ in 0..64 {
            x = x *% 1664525 +% 1013904223
            w := TrWord(mut buf, x, n)
            m := re.Find(w)
            v := vm.Find(w)
            Assert(m.IsSome() == v.IsSome())
            if m.IsSome() {
                Assert(m.Must().lo == v.Must().lo && m.Must().hi == v.Must().hi)
            }
        }
    }
}

test "names and the prefix outlive Compile's sub-arena" (sys: &System) {
    re := Compile(sys.arena, "ab(?P<word>c)(?<tail>d+)") else {
        Assert(false)
        return
    }
    var junk := sys.arena.Sub() else {
        Assert(false)
        return
    }
    defer junk.Free()
    fill := junk.Slice[u8](1 << 16) else {
        Assert(false)
        return
    }
    fill.Fill(0x7A)
    Assert(re.prefix == "abc")
    Assert(re.names.Len() == 3 && re.names[0] == "" && re.names[1] == "word" && re.names[2] == "tail")
    m := re.Find("xxabcdd").Must()
    Assert(m.lo == 2 && m.hi == 7)
    Assert(re.Group("xxabcdd", m, 2).Must() == "dd")
}

// TrKept is the bytes a Regex's own tables take: the program, both DFAs'
// classes and transitions, and the group names.
TrKept(re: &Regex): int {
    var n := re.insts.Len() * SizeOf[Inst]() + re.edges.Len() * SizeOf[Edge]()
    n += re.dfa.cls.Len() + re.dfa.trans.Len() * 4
    n += re.rdfa.cls.Len() + re.rdfa.trans.Len() * 4
    return n + re.names.Len() * SizeOf[str]()
}

test "Compile keeps only the Regex's tables in its arena" (sys: &System) {
    // A 2,050-state DFA, whose build scratch (the parse tree, the reverse
    // program, the subset pools) would take megabytes if it stayed.
    var a := sys.arena.Sub() else {
        Assert(false)
        return
    }
    defer a.Free()
    re := Compile(&a, "[ab]*a[ab]{9}") else {
        Assert(false)
        return
    }
    Assert(re.dfa.ok && re.rdfa.ok)
    kept := TrKept(&re)
    held := core.ArenaHeld(&a)
    // Chunks double from 4 KiB, so the arena holds under twice what it
    // keeps, plus the first chunk (58,420 bytes kept and 61,512 held when
    // written, Windows, dev).
    Assert(held <= 2 * kept + 4_096)
}
