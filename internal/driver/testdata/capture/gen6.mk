G(const n: int): int {
	return n * 2
}

F(const n: int): int {
	return G(n) + 1
}

export Main(sys: &System): WriteError!void {
	var out := sys.Stdout()
	r := F(5)
	try Print(mut out, "{r}\n", (r))
}
