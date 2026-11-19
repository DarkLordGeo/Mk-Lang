// verdict: reject E0411
// prelude: yes
error Full

Hold(r: Res, room: bool): Full!void {
    Take(r) // <- E0411
    if !room {
        fail Full
    }
}
