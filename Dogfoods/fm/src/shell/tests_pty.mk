// Why: a command's terminal output is shown on fm's status line.
// What: tests of Clean, TrimBlank and FirstLine.
// How: control sequences, carriage returns and blank lines are dropped.

test "Clean drops control sequences and carriage returns" (sys: &System) {
    s := Clean(sys.arena, b"\x1b[2J\x1b[H\x1b]0;title\x07hello\r\n\x1b[?25h").Must()
    Assert(s == "hello\n")
}

test "TrimBlank drops blank lines around the output and keeps its indentation" {
    Assert(TrimBlank("\n   \n  hello\nworld  \n\n") == "  hello\nworld")
    Assert(TrimBlank("   \n") == "")
}

test "FirstLine skips blank lines" {
    Assert(FirstLine("\n  \n x \ny") == "x")
}
