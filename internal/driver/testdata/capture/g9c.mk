Apply2[T: Plain](xs: []T, const f: fn(&T): i32, const g: fn(&T): i32): i32 {
	var s: i32 = 0
	for x in xs {
		s += f(&x) * 10 + g(&x)
	}
	return s
}

Fwd[T: Plain](xs: []T, m: i32, const f: fn(&T): i32): i32 {
	return Apply2(xs, f, fn(x: &T): i32 { return 7 })
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	xs: [3]i32 = [1, 2, 3]
	k: i32 = 6
	r := Fwd(xs[..], 3, fn(x: &i32): i32 { return *x + k })
	try Print(mut out, "{r}\n", (r))
}
