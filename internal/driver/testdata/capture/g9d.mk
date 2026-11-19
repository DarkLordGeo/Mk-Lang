Ap3[T: Plain](xs: []T, const f: fn(&T): i32, n: i32): i32 {
	var s: i32 = n
	for x in xs {
		s += f(&x)
	}
	return s
}

Compute(m: i32): i32 {
	return m * 100
}

Fwd[T: Plain](xs: []T, m: i32, const f: fn(&T): i32): i32 {
	return Ap3(xs, f, Compute(m))
}

FwdN(xs: []i32, m: i32, const f: fn(&i32): i32): i32 {
	return Ap3(xs, f, Compute(m)) + Fwd(xs, m, f)
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	xs: [3]i32 = [1, 2, 3]
	k: i32 = 6
	r := Fwd(xs[..], 3, fn(x: &i32): i32 { return *x + k })
	q := FwdN(xs[..], 1, fn(x: &i32): i32 { return *x * k })
	try Print(mut out, "{r} {q}\n", (r, q))
}
