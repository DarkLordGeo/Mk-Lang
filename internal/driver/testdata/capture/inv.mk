Ap(const f: fn(): bool): bool {
	return f()
}

struct Span {
	lo: int
	hi: int
	invariant Ap(fn(): bool { return lo <= hi })
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	s := Span(lo: 1, hi: 3)
	try Print(mut out, "{n}\n", (n: s.hi - s.lo))
}
