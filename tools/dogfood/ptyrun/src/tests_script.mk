// Why: a script that parses wrong types the wrong keys and fails a case for
// a reason that is not the program's.
// What: tests of the script parser (script.mk): each step, the escapes, the
// key table, and each error with its line.
// How: parses literal scripts and compares the steps and bytes, or the
// failing line number.

SameBytes(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

InputOf(s: &Script, k: int): []u8 from s {
    match s.steps[k].act {
        .Input(bytes) { return bytes }
        _ { Panic("not an input step") }
    }
}

test "a script's steps parse in order, with their lines, the size and the default size" (sys: &System) {
    s := Parse(sys.arena, "# a comment\n\nsize 100 30\nexpect 1\nkeys ab c\\r\nresize 90 20\nsleep 50\n").Must()
    Assert(s.cols == 100 && s.rows == 30)
    Assert(s.steps.Len() == 4)
    Assert(s.steps[0].line == 4)
    match s.steps[0].act {
        .Expect(lines) { Assert(lines == 1) }
        _ { Panic("step 0 is not expect") }
    }
    var want: [5]u8 = [b'a', b'b', b' ', b'c', 13]
    Assert(SameBytes(InputOf(&s, 1), want[..]))
    match s.steps[2].act {
        .Resize(cols, rows) { Assert(cols == 90 && rows == 20) }
        _ { Panic("step 2 is not resize") }
    }
    match s.steps[3].act {
        .Sleep(ms) { Assert(ms == 50) }
        _ { Panic("step 3 is not sleep") }
    }
    d := Parse(sys.arena, "expect 2\n").Must()
    Assert(d.cols == 80 && d.rows == 24)
}

test "escapes decode to their bytes, and UTF-8 passes through" (sys: &System) {
    s := Parse(sys.arena, "keys \\e\\r\\n\\t\\\\\\x41\\x7f\nkeys é\n").Must()
    var want: [7]u8 = [0x1b, 13, 10, 9, 92, 0x41, 0x7f]
    Assert(SameBytes(InputOf(&s, 0), want[..]))
    var utf: [2]u8 = [0xc3, 0xa9]
    Assert(SameBytes(InputOf(&s, 1), utf[..]))
}

test "named keys, ctrl keys and a paste give xterm's bytes" (sys: &System) {
    s := Parse(sys.arena, "key up enter ctrl-c f5\npaste a\\nb\n").Must()
    var want: [9]u8 = [0x1b, b'[', b'A', 13, 3, 0x1b, b'[', b'1', b'5']
    k := InputOf(&s, 0)
    Assert(k.Len() == 10 && SameBytes(k[0..9], want[..]) && k[9] == b'~')
    p := InputOf(&s, 1)
    Assert(p.Len() == 15 && p[0] == 0x1b && p[5] == b'~' && p[6] == b'a' && p[7] == 10 && p[8] == b'b' && p[14] == b'~')
}

ErrLine(e: ScriptError): int {
    return match e {
        BadStep(line) { line }
        BadNumber(line) { line }
        BadEscape(line) { line }
        UnknownKey(line) { line }
        LateSize(line) { line }
        OutOfMemory { -1 }
    }
}

FailsAt(a: &Arena, text: str): int {
    _ = Parse(a, text) else e {
        return ErrLine(e)
    }
    return 0
}

test "each malformed line fails with its line number" (sys: &System) {
    a := sys.arena
    Assert(FailsAt(a, "type x\n") == 1)
    Assert(FailsAt(a, "expect 1\nexpect\n") == 2)
    Assert(FailsAt(a, "expect 0\n") == 1)
    Assert(FailsAt(a, "sleep 60001\n") == 1)
    Assert(FailsAt(a, "size 80\n") == 1)
    Assert(FailsAt(a, "size 0 24\n") == 1)
    Assert(FailsAt(a, "resize 80 24 1\n") == 1)
    Assert(FailsAt(a, "keys a\\q\n") == 1)
    Assert(FailsAt(a, "keys a\\x4\n") == 1)
    Assert(FailsAt(a, "keys\n") == 1)
    Assert(FailsAt(a, "key enter shift\n") == 1)
    Assert(FailsAt(a, "key ctrl-1\n") == 1)
    Assert(FailsAt(a, "expect 1\nsize 80 24\n") == 2)
    match Parse(a, "keys a\\q\n") {
        ok(_) { Panic("parsed") }
        err(e) {
            match e {
                BadEscape {}
                _ { Panic("not BadEscape") }
            }
        }
    }
}
