export Main(sys: &System): OutOfMemory|WriteError!void {
	var out := sys.Stdout()
	var a := try Arena.New(sys.heap)
	defer a.Free()
	var l := List[i32].New(&a)
	try l.Push(4)
	try l.Push(9)
	try l.Push(2)
	try l.Push(7)
	origin: i32 = 5
	l.Sort(fn(x: &i32, y: &i32): bool {
		dx := *x - origin
		dy := *y - origin
		return dx * dx < dy * dy
	})
	for x in l.Items() {
		try Print(mut out, "{x} ", (x))
	}
	try Print(mut out, "\n")
}
