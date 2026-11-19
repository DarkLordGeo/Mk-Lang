// Why: globs decide which files are searched.
// What: tests of Glob and AnyGlob.
// How: stars, question marks and comma-separated lists.

test "a star matches any run of bytes, an empty one included" {
    Assert(Glob("*.txt", "notes.txt"))
    Assert(Glob("*.txt", ".txt"))
    Assert(!Glob("*.txt", "notes.md"))
}

test "a question mark matches exactly one byte" {
    Assert(Glob("f?.txt", "f1.txt"))
    Assert(!Glob("f?.txt", "f.txt"))
}

test "comma-separated globs match when any one does" {
    globs := ["*.md,*.dat"]
    Assert(AnyGlob(globs[..], "b.md"))
    Assert(AnyGlob(globs[..], "bin.dat"))
    Assert(!AnyGlob(globs[..], "a.txt"))
}
