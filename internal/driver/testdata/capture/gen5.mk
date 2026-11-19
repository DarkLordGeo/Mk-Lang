Ap(xs: []i32, const f: fn(&i32): i32): i32 {
	var s: i32 = 0
	for x in xs {
		s += f(&x)
	}
	return s
}

Fwd(xs: []i32, const f: fn(&i32): i32): i32 {
	return Ap(xs, f) + Ap(xs, f)
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	xs: [3]i32 = [1, 2, 3]
	k: i32 = 10
	r := Fwd(xs[..], fn(x: &i32): i32 { return *x + k })
	try Print(mut out, "{r}\n", (r))
}
