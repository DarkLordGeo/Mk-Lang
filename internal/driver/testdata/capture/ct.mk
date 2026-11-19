Closest(origin: i32): int {
	var xs: [4]i32 = [9, 2, 6, 4]
	xs.Sort(fn(a: &i32, b: &i32): bool {
		da := *a - origin
		db := *b - origin
		return da * da < db * db
	})
	return Cast[int](xs[0])
}

const C: int = Closest(4)

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	try Print(mut out, "{c} {r}\n", (c: C, r: Closest(4)))
}
