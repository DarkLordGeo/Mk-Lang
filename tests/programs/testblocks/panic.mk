// mk test: "panics" panics in its own child process; "after" still runs and passes; `mk test` exits 1.
test "panics" {
    Panic("boom")
}

test "after" {
    Assert(true)
}
