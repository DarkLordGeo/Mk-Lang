// Why: Path holds OS bytes that may not be UTF-8, with Windows and Unix
// rules; its edge cases are easy to get wrong.
// What: test blocks for the prelude Path's Base, Parent, Join, Str and Format.
// How: run by `mk test --std=std.core` (std/std-missing_blocks_test.go); the
// Go model over adversarial inputs is std/std-missing_path_test.go.

PathText(p: &Path): str from p {
    s := p.Str() else {
        return "<not utf-8>"
    }
    return s
}

test "Path.New copies, and AsBytes and Str read the bytes back" (sys: &System) {
    var src: [3]u8 = [97, 47, 98]
    p := Path.New(sys.arena, src[..]) else {
        Assert(false)
        return
    }
    src[0] = 120
    Assert(PathText(&p) == "a/b")
    Assert(p.AsBytes().Len() == 3)
    var bad: [2]u8 = [0xFF, 97]
    q := Path.New(sys.arena, bad[..]) else {
        Assert(false)
        return
    }
    Assert(q.Str().IsNone())
    Assert(q.AsBytes()[0] == 0xFF)
    e := Path.New(sys.arena, src[0..0]) else {
        Assert(false)
        return
    }
    Assert(e.AsBytes().Len() == 0)
}

PathFrom(a: &Arena, s: str): Path from a {
    return Path.New(a, s.AsBytes()).Must()
}

BaseIs(a: &Arena, s: str, want: str): bool {
    p := PathFrom(a, s)
    b := p.Base()
    return PathText(&b) == want
}

ParentIs(a: &Arena, s: str, want: str): bool {
    p := PathFrom(a, s)
    q := p.Parent() else {
        return want == "<none>"
    }
    return PathText(&q) == want
}

JoinIs(a: &Arena, s: str, part: str, want: str): bool {
    p := PathFrom(a, s)
    j := p.Join(a, part).Must()
    return PathText(&j) == want
}

test "Path.Base gives the last part" (sys: &System) {
    a := sys.arena
    Assert(BaseIs(a, "", "."))
    Assert(BaseIs(a, "a", "a"))
    Assert(BaseIs(a, "a/b", "b"))
    Assert(BaseIs(a, "a/b/", "b"))
    Assert(BaseIs(a, "a/b//", "b"))
    Assert(BaseIs(a, "/", "/"))
    Assert(BaseIs(a, "///", "/"))
    Assert(BaseIs(a, "/x", "x"))
}

test "Path.Parent drops the last part" (sys: &System) {
    a := sys.arena
    Assert(ParentIs(a, "", "<none>"))
    Assert(ParentIs(a, "a", "<none>"))
    Assert(ParentIs(a, "/", "<none>"))
    Assert(ParentIs(a, "a/b", "a"))
    Assert(ParentIs(a, "a//b/", "a"))
    Assert(ParentIs(a, "/a", "/"))
    Assert(ParentIs(a, "/a/b/c", "/a/b"))
    Assert(ParentIs(a, "//a", "//"))
}

test "Path.Join adds one separator, and a rooted part replaces the path" (sys: &System) {
    a := sys.arena
    sep := if Target.os == Os.Windows { "\\" } else { "/" }
    Assert(JoinIs(a, "a", "b", a.Fmt("a{s}b", (s: sep)).Must()))
    Assert(JoinIs(a, "a/", "b", "a/b"))
    Assert(JoinIs(a, "", "b", "b"))
    Assert(JoinIs(a, "a", "", "a"))
    Assert(JoinIs(a, "a", "/b", "/b"))
}

test "Path keeps a Windows drive in Base, Parent and Join" (sys: &System) {
    if Target.os != Os.Windows {
        return
    }
    a := sys.arena
    Assert(BaseIs(a, "C:", "."))
    Assert(BaseIs(a, "C:\\", "\\"))
    Assert(BaseIs(a, "C:\\x\\y", "y"))
    Assert(BaseIs(a, "a\\b", "b"))
    Assert(ParentIs(a, "C:\\", "<none>"))
    Assert(ParentIs(a, "C:\\x", "C:\\"))
    Assert(ParentIs(a, "C:x", "C:"))
    Assert(ParentIs(a, "a\\b", "a"))
    Assert(JoinIs(a, "C:", "x", "C:x"))
    Assert(JoinIs(a, "a", "D:\\y", "D:\\y"))
}

// A part with a root and no drive keeps the path's drive, as Rust's
// PathBuf::push documents ("if path has a root but no prefix (e.g.,
// \windows), it replaces everything except for the prefix (if any) of
// self"); replacing the drive too would make "C:\x" and "\y" name a file
// on the current drive.
test "Path.Join of a rooted part without a drive keeps the path's drive" (sys: &System) {
    if Target.os != Os.Windows {
        return
    }
    a := sys.arena
    j := Path.New(a, "C:\\x".AsBytes()).Must().Join(a, "\\y").Must()
    Assert(j.Str().Must() == "C:\\y")
    Assert(JoinIs(a, "C:\\x", "/y", "C:/y"))
    Assert(JoinIs(a, "C:", "\\y", "C:\\y"))
    Assert(JoinIs(a, "c:x\\z", "\\y\\w", "c:\\y\\w"))
    Assert(JoinIs(a, "\\x", "\\y", "\\y"))
    Assert(JoinIs(a, "x", "\\y", "\\y"))
    // A part with a drive, rooted or not, replaces the whole path.
    Assert(JoinIs(a, "C:\\x", "D:y", "D:y"))
    Assert(JoinIs(a, "C:\\x", "C:", "C:"))
}

test "Path.Format replaces each maximal ill-formed subpart" (sys: &System) {
    a := sys.arena
    var t := Text.New(a)
    // a, F0 9F 98 (a truncated 4-byte sequence: one subpart), b, FF, C0 80
    // (two bytes, each its own subpart), ED A0 80 (a surrogate: three).
    var raw: [11]u8 = [97, 0xF0, 0x9F, 0x98, 98, 0xFF, 0xC0, 0x80, 0xED, 0xA0, 0x80]
    p := Path.New(a, raw[..]).Must()
    var w := t.Writer()
    p.Format(mut w).Must()
    Assert(t.Str() == "a�b������")
}

test "Path formats through Print" (sys: &System) {
    a := sys.arena
    var t := Text.New(a)
    p := PathFrom(a, "dir/é")
    var w := t.Writer()
    Print(mut w, "[{p}]", (p: &p)).Must()
    Assert(t.Str() == "[dir/é]")
}
