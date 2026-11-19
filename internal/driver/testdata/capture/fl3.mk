Ap(const f: fn(): i32): i32 {
	return f()
}

Count[A: Fields[Format] + Plain](args: A): i32 {
	var n: i32 = 0
	for _, _ in fields(&args) {
		n += Ap(fn(): i32 {
			z: i32 = 3
			return Ap(fn(): i32 { return z })
		})
	}
	return n
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	r := Count((a: 1, b: 2))
	try Print(mut out, "{r}\n", (r))
}
