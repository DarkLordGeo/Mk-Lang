// Why: path cleaning decides which directory every command acts on.
// What: tests of Clean, Parent and BaseName.
// How: `..` handling, roots and drives, and relative paths.

test "Clean takes each .. with the element before it" (sys: &System) {
    Assert(Clean(sys.arena, "demo/left/../right").Must() == "demo/right")
    Assert(Clean(sys.arena, "a/./b//c/").Must() == "a/b/c")
}

test "Clean keeps a root and a drive, and drops a .. at the root" (sys: &System) {
    Assert(Clean(sys.arena, "/a/../../b").Must() == "/b")
    Assert(Clean(sys.arena, r"C:\x\..\y").Must() == "C:/y")
}

test "Clean keeps a leading .. of a relative path, and gives . for nothing" (sys: &System) {
    Assert(Clean(sys.arena, "../a").Must() == "../a")
    Assert(Clean(sys.arena, "a/..").Must() == ".")
}

test "Parent stops at a root and BaseName drops trailing separators" {
    Assert(Parent("/a/b").Must() == "/a")
    Assert(Parent("/a").Must() == "/")
    Assert(Parent("/").IsNone())
    Assert(Parent(r"C:\x").Must() == r"C:\")
    Assert(BaseName("dir/sub/") == "sub")
}
