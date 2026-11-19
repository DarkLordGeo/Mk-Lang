// Why: a container call refused for memory must change nothing and leak
// nothing.
// What: out-of-memory tests of HeapText.Set, TextMap.Set and Replace, and
// HeapMap.Replace.
// How: each runs over a Counting over a Fixed buffer, which refuses at its
// budget. Run by `mk test --std=std.mem`. Helper names start with To.

// ToAlpha gives distinct keys by length: ToAlpha[..i + 1].
const ToAlpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

test "HeapText.Set refused by the allocator keeps the text" {
    var buf: [56]u8 = [0; 56]
    lead := TmLead(buf[..])
    f := Fixed.New(mut buf[lead..lead + 48])
    c := Counting.New(f.Alloc())
    var t := HeapText.New(c.Alloc())
    t.Set(c.Alloc(), "keep") else {
        Assert(false)
    }
    live := c.Live()
    var refused := false
    t.Set(c.Alloc(), ToAlpha) else {
        refused = true
    }
    Assert(refused && t.Str() == "keep" && t.Len() == 4 && c.Live() == live)
    t.Set(c.Alloc(), "kept") else {
        Assert(false)
    }
    Assert(t.Str() == "kept")
    t.Free(c.Alloc())
    Assert(c.Live() == 0)
}

test "TextMap.Set and Replace refused by the allocator change nothing and leak no key copy" {
    var buf: [1032]u8 = [0; 1032]
    lead := TmLead(buf[..])
    f := Fixed.New(mut buf[lead..lead + 1024])
    c := Counting.New(f.Alloc())
    var m := TextMap[int].New(c.Alloc())
    var held := -1
    for i in 0..ToAlpha.Len() {
        live := c.Live()
        m.Set(c.Alloc(), ToAlpha[..i + 1], i) else {
            Assert(c.Live() == live && m.Len() == i)
            held = i
            break
        }
    }
    Assert(held > 0)
    var refused := false
    r := m.Replace(c.Alloc(), ToAlpha[..held + 1], 99) else {
        refused = true
        none
    }
    Assert(refused && r.IsNone() && m.Len() == held && m.Get(ToAlpha[..held + 1]).IsNone())
    // A present key needs no memory, so it is not refused.
    old := m.Replace(c.Alloc(), ToAlpha[..1], 7) else {
        Assert(false)
        none
    }
    Assert(old.Must() == 0 && *m.Get("a").Must() == 7)
    m.Set(c.Alloc(), ToAlpha[..2], 8) else {
        Assert(false)
    }
    Assert(*m.Get("ab").Must() == 8)
    m.Free(c.Alloc())
    Assert(c.Live() == 0)
}

test "HeapMap.Replace refused by the allocator changes nothing" {
    var buf: [1032]u8 = [0; 1032]
    lead := TmLead(buf[..])
    f := Fixed.New(mut buf[lead..lead + 1024])
    c := Counting.New(f.Alloc())
    var m := HeapMap[int, int].New(c.Alloc())
    var held := -1
    for i in 0..10_000 {
        _ = m.Replace(c.Alloc(), i, i * 3) else {
            Assert(m.Len() == i)
            held = i
            break
        }
    }
    Assert(held > 0 && m.Get(&held).IsNone())
    for i in 0..held {
        old := m.Replace(c.Alloc(), i, -i) else {
            Assert(false)
            none
        }
        Assert(old.Must() == i * 3)
    }
    m.Free(c.Alloc())
    Assert(c.Live() == 0)
}
