export Main(sys: &System): WriteError!void {
    let mut xs = List[i32].New(sys.arena);
    xs.push(1);
    var out := sys.Stdout()
    try Print(mut out, "{n}\n", (n: xs.len()))
}
