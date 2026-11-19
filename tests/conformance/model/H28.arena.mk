// verdict: reject E0505
// case: H28.arena
// note: same shape as H28, on a parameter arena. The push stores &n into a, which outlives n
Late(a: &Arena): OutOfMemory!void {
    var list := List[&i32].New(a)
    n: i32 = 1
    try list.Push(&n) // <- E0505
}
