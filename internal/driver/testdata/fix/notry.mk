Fill(a: &Arena): OutOfMemory!void {
    var xs := List[i32].New(a)
    xs.Push(1)
}
