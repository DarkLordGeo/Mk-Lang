Any(xs: []i32, const keep: fn(&i32): bool): bool {
	for x in xs {
		if keep(&x) {
			return true
		}
	}
	return false
}

Has(xs: []i32, limit: i32): bool
	requires Any(xs, fn(x: &i32): bool { return *x > limit })
{
	return true
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	xs: [3]i32 = [1, 5, 9]
	r := Has(xs[..], 20)
	try Print(mut out, "{r}\n", (r))
}
