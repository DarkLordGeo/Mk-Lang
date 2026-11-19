All(xs: []i32, const keep: fn(&i32): bool): bool {
	for x in xs {
		if !keep(&x) {
			return false
		}
	}
	return true
}

Max(xs: []i32): i32
	ensures All(xs, fn(x: &i32): bool { return *x <= result })
{
	var m: i32 = 0
	for x in xs {
		if x > m {
			m = x
		}
	}
	return m
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	xs: [3]i32 = [1, 5, 9]
	r := Max(xs[..])
	try Print(mut out, "{r}\n", (r))
}
