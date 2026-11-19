// Why: a caller through an interface bound sees only the interface method,
// which has no contracts, so an impl method's compile-time requires would
// never be judged.
// What: TestImplRequires, TestImplOwnConstGeneric,
// TestImplTypeGeneric.
// How: such a clause over the impl's or the method's generics is E0706 at
// the clause; inherent methods and run-time clauses stay allowed.

package check

import "testing"

func TestImplRequires(t *testing.T) {
	t.Parallel()
	const s = "interface I {\n\tM(&self): int\n}\n\nstruct S[const N: int] {\n\ta: int\n}\n\n"         // lines 1-8
	const j = "interface J {\n\tM(&self, const k: int, n: int): int\n}\n\nstruct R {\n\ta: int\n}\n\n" // lines 1-8
	const jm = "impl J for R {\n\tM(&self, const k: int, n: int): int\n\t\trequires "
	const end = " {\n\t\treturn n\n\t}\n}\n"
	runBodyCases(t, []bodyCase{
		// Reached as H[S[0]] through `T: I`.
		{"impl const generic", s + "impl[const N: int] I for S[N] {\n\tM(&self): int\n\t\trequires N > 0 {\n\t\treturn N\n\t}\n}\n", "E0706@11:3"},
		{"method const parameter", j + jm + "k < 4" + end, "E0706@11:3"},
		{"run-time clause", j + jm + "n > 0" + end, ""},
		{"const parameter beside a value one is run-time", j + jm + "k < n" + end, ""},
		{"consts and literals", "const Max = 1\n\n" + j + jm + "Max > 0" + end, ""},
		// A method of no impl keeps its compile-time clause.
		{"no impl", "struct S[const N: int] {\n\ta: int\n}\n\nS[N].Z(): int\n\trequires N > 0 {\n\treturn N\n}\n", ""},
	})
}

// TestImplOwnConstGeneric: a clause of an impl method over the
// method's own const generics is E0706 at the clause too. H[X: I] calling
// `x.M[24](t)` judges no clause of the impl method, whose body may rely on
// it through a skip, an implied set, a listed set or the sole-clause exit;
// a release build then read past a table. An inherent method, and an impl
// method's clause over its type generics, stay allowed.
func TestImplOwnConstGeneric(t *testing.T) {
	t.Parallel()
	const k = "interface K {\n\tM[const V: int](&self): int\n}\n\nstruct R {\n\ta: int\n}\n\n" // lines 1-8
	const km = "impl K for R {\n\tM[const V: int](&self): int\n"                               // lines 9-10
	const end = " {\n\t\treturn V\n\t}\n}\n"
	runBodyCases(t, []bodyCase{
		{"sole clause (b1)", k + km + "\t\trequires V >= 0 && V < 4" + end, "E0706@11:3"},
		{"listed (b5)", k + km + "\t\trequires V == 3" + end, "E0706@11:3"},
		{"skip pair (b2)", k + km + "\t\trequires V == 3 || V == 24\n\t\trequires V < 4" + end, "E0706@11:3 E0706@12:3"},
		{"two generics, implied (b3)", "interface K {\n\tM[const N: int, const V: int](&self): int\n}\n\nstruct R {\n\ta: int\n}\n\n" +
			"impl K for R {\n\tM[const N: int, const V: int](&self): int\n\t\trequires N == 2 || N == 4\n\t\trequires V == N / 2" + end, "E0706@11:3 E0706@12:3"},
		{"beside a run-time parameter", "interface K {\n\tM[const V: int](&self, n: int): int\n}\n\nstruct R {\n\ta: int\n}\n\n" +
			"impl K for R {\n\tM[const V: int](&self, n: int): int\n\t\trequires V < n" + end, ""},
		{"inherent method", "struct R {\n\ta: int\n}\n\nR.M[const V: int](&self): int\n\trequires V >= 0 && V < 4 {\n\treturn V\n}\n", ""},
	})
}

// TestImplTypeGeneric: a compile-time clause of an impl method over
// any type generic, the method's own (`x.M[Big](t)` through a bound) or the
// impl's (`impl[T] I for W[T]` reached as `H[W[Big]]`), is E0706 at the
// clause, since a caller through the bound judges none of the impl method's
// clauses while its body may rely on one, as on a restated `SizeOf[U]()`. A
// run-time clause beside a type generic and an inherent method's clause
// stay allowed.
func TestImplTypeGeneric(t *testing.T) {
	t.Parallel()
	const own = "interface K {\n\tM[U](&self): int\n}\n\nstruct R {\n\ta: int\n}\n\n" // lines 1-8
	const owner = "interface K {\n\tM(&self, n: int): int\n}\n\nstruct R[T] {\n\ta: int\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"own type generic (n02)", own + "impl K for R {\n\tM[U](&self): int\n\t\trequires SizeOf[U]() < 4 {\n\t\treturn 1\n\t}\n}\n", "E0706@11:3"},
		{"own type generic, AlignOf", own + "impl K for R {\n\tM[U](&self): int\n\t\trequires AlignOf[U]() == 1 {\n\t\treturn 1\n\t}\n}\n", "E0706@11:3"},
		{"type generic of the impl (n02b)", owner + "impl[T] K for R[T] {\n\tM(&self, n: int): int\n\t\trequires SizeOf[T]() > 0 {\n\t\treturn 1\n\t}\n}\n", "E0706@11:3"},
		{"beside a run-time parameter", owner + "impl[T] K for R[T] {\n\tM(&self, n: int): int\n\t\trequires SizeOf[T]() < n {\n\t\treturn 1\n\t}\n}\n", ""},
		{"inherent method", "struct R {\n\ta: int\n}\n\nR.M[U](&self): int\n\trequires SizeOf[U]() < 4 {\n\treturn 1\n}\n", ""},
		{"inherent method of a generic type", "struct R[T] {\n\ta: int\n}\n\nR[T].M(&self): int\n\trequires SizeOf[T]() < 4 {\n\treturn 1\n}\n", ""},
	})
}
