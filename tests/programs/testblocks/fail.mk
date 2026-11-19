// mk test: "holds" passes and "fails" fails its assertion, so `mk test` exits 1 on this file.
test "holds" {
    Assert(1 + 1 == 2)
}

test "fails" {
    Assert(1 + 1 == 3)
}
