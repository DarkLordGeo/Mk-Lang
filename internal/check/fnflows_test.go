// Why: a fn value given where a fn type is expected must not carry flows
// the type does not declare.
// What: TestE0608_FnValue.
// How: checks a conversion inside a body; flows compare before substitution.

package check

import "testing"

func TestE0608_FnValue(t *testing.T) {
	const two = "First(a: &i32, b: &i32): &i32 from a {\n\treturn a\n}\n\nSecond(a: &i32, b: &i32): &i32 from b {\n\treturn b\n}\n\nTake(f: fn(a: &i32, b: &i32): &i32 from a) {\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"H53 generic result declares none", "Id(x: &i32): &i32 {\n\treturn x\n}\n\nApply[T](f: fn(&i32): T): int {\n\treturn 0\n}\n\nRun(): int {\n\treturn Apply(Id)\n}\n", "E0608@10:15"},
		{"H53 with the flow declared", "Id(x: &i32): &i32 {\n\treturn x\n}\n\nApply[T](f: fn(x: &i32): T from x): int {\n\treturn 0\n}\n\nRun(): int {\n\treturn Apply(Id)\n}\n", ""},
		{"automatic source on a closed fn type", "Id(x: &i32): &i32 {\n\treturn x\n}\n\nApply(f: fn(&i32): &i32): int {\n\treturn 0\n}\n\nRun(): int {\n\treturn Apply(Id)\n}\n", ""},
		{"same flows", two + "Run() {\n\tTake(First)\n}\n", ""},
		{"other parameter", two + "Run() {\n\tTake(Second)\n}\n", "E0608@13:7"},
		{"typed binding", two + "Run() {\n\tf: fn(a: &i32, b: &i32): &i32 from a = Second\n\tTake(f)\n}\n", "E0608@13:41"},
		{"other shape is E0301", two + "Third(a: &i32): &i32 {\n\treturn a\n}\n\nRun() {\n\tTake(Third)\n}\n", "E0301@17:7"},
	})
}
