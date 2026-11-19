// Why: the compile-time evaluator and the native code must agree on type
// layouts and on arithmetic; a difference is a miscompile that no single
// side can see.
//
// What: the reader of tests/conformance/diff/layout.<target>.golden
// (strict), LayoutCheckSource and LayoutProgramSource, and for diff/eval.mk
// EvalCases, EvalCheckSource and EvalProgramSource.
//
// How: layout.mk is extended with one compile-time check per golden row, so
// the evaluator compares SizeOf and AlignOf with the golden while the driver
// checks the file (a same-module const may call same-module functions). A
// disagreeing row is an E0901 at that row's const, whose panic is "assert".
// The program forms do the same comparisons at run time.

package corpus

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DiffDir is the differential suites' directory, repository-relative.
const DiffDir = "tests/conformance/diff"

// LayoutRow is one line of a layout golden: the type behind function
// Layout<Name>, its size, its alignment and its fields' offsets.
type LayoutRow struct {
	Name        string
	Size, Align int
	Offsets     []int
	Line        int // in the golden
}

// ParseLayoutGolden reads a golden: '#' lines and blank lines are skipped;
// every other line is `<Name> <size> <align> [<offset>...]` with a
// non-negative size, a power-of-two alignment, and each offset below the
// size. A name may appear once. Errors name path:line.
func ParseLayoutGolden(path string, data []byte) ([]LayoutRow, error) {
	var rows []LayoutRow
	seen := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("%s:%d: want <Name> <size> <align> [<offset>...], got %q", path, n, line)
		}
		if !layoutName.MatchString(f[0]) {
			return nil, fmt.Errorf("%s:%d: %q is not a type name", path, n, f[0])
		}
		if prev, dup := seen[f[0]]; dup {
			return nil, fmt.Errorf("%s:%d: %s is also on line %d", path, n, f[0], prev)
		}
		seen[f[0]] = n
		nums := make([]int, len(f)-1)
		for i, s := range f[1:] {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 {
				return nil, fmt.Errorf("%s:%d: %q is not a non-negative integer", path, n, s)
			}
			nums[i] = v
		}
		r := LayoutRow{Name: f[0], Size: nums[0], Align: nums[1], Offsets: nums[2:], Line: n}
		if r.Align == 0 || r.Align&(r.Align-1) != 0 {
			return nil, fmt.Errorf("%s:%d: alignment %d is not a power of two", path, n, r.Align)
		}
		for _, o := range r.Offsets {
			if o >= r.Size {
				return nil, fmt.Errorf("%s:%d: offset %d is not below the size %d", path, n, o, r.Size)
			}
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: no rows", path)
	}
	return rows, nil
}

var (
	layoutName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	layoutFunc = regexp.MustCompile(`(?m)^Layout([A-Z][A-Za-z0-9]*)\(\): Layout \{`)
)

// LayoutFuncs lists the <Name> of every top-level function Layout<Name>()
// of layout.mk, in source order.
func LayoutFuncs(src []byte) []string {
	var names []string
	for _, m := range layoutFunc.FindAllSubmatch(src, -1) {
		names = append(names, string(m[1]))
	}
	return names
}

// diffWant is the checking function LayoutCheckSource appends. Its name and
// those of the consts start with Diff, which layout.mk does not use.
const diffWant = `
// Appended by internal/corpus (TestDiffLayout): one compile-time check per golden row.
DiffWantLayout(l: Layout, size: int, align: int): bool {
    Assert(l.size == size)
    Assert(l.align == align)
    return true
}
`

// LayoutCheckSource returns layout.mk's source with diffWant and one const
// per row appended, `const DiffWant<Name>: bool = DiffWantLayout(Layout<Name>(), size, align)`,
// and the 1-based line of each row's const.
func LayoutCheckSource(src []byte, rows []LayoutRow) ([]byte, map[int]LayoutRow) {
	var b bytes.Buffer
	b.Write(src)
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(diffWant)
	b.WriteByte('\n')
	line := bytes.Count(b.Bytes(), []byte("\n")) + 1
	at := make(map[int]LayoutRow, len(rows))
	for _, r := range rows {
		fmt.Fprintf(&b, "const DiffWant%s: bool = DiffWantLayout(Layout%s(), %d, %d)\n", r.Name, r.Name, r.Size, r.Align)
		at[line] = r
		line++
	}
	return b.Bytes(), at
}

// EvalCase is one function Case<Name>(): T of diff/eval.mk (for
// TestDiffEvalNative): its name without the prefix, its result type as
// written, and its 1-based line in eval.mk.
type EvalCase struct {
	Name string
	Type string
	Line int
}

// Trap reports whether the case must panic in both executions: eval.mk's
// convention is a name ending in Trap.
func (c EvalCase) Trap() bool { return strings.HasSuffix(c.Name, "Trap") }

var evalFunc = regexp.MustCompile(`^Case([A-Za-z0-9_]*)\((.*?)\)(?:: (.*?))? \{$`)

// EvalCases lists eval.mk's top-level functions whose name starts with
// Case, in source order. Each must be `Case<Name>(): T {` on one line with
// an upper-case <Name>, no parameter and a result type, and a name may
// appear once. Errors name path:line.
func EvalCases(path string, src []byte) ([]EvalCase, error) {
	var cases []EvalCase
	seen := map[string]int{}
	for n, line := range strings.Split(string(src), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "Case") {
			continue
		}
		m := evalFunc.FindStringSubmatch(line)
		switch {
		case m == nil:
			return nil, fmt.Errorf("%s:%d: want `Case<Name>(): T {`, got %q", path, n+1, line)
		case !layoutName.MatchString(m[1]):
			return nil, fmt.Errorf("%s:%d: %q is not an upper-case case name", path, n+1, m[1])
		case m[2] != "":
			return nil, fmt.Errorf("%s:%d: Case%s takes parameters (%s); a case takes none", path, n+1, m[1], m[2])
		case m[3] == "":
			return nil, fmt.Errorf("%s:%d: Case%s has no result type", path, n+1, m[1])
		}
		if prev, dup := seen[m[1]]; dup {
			return nil, fmt.Errorf("%s:%d: Case%s is also on line %d", path, n+1, m[1], prev)
		}
		seen[m[1]] = n + 1
		cases = append(cases, EvalCase{Name: m[1], Type: m[3], Line: n + 1})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%s: no Case<Name> function", path)
	}
	return cases, nil
}

// diffAfter is the function EvalCheckSource appends when after is set: it
// returns its argument only after an assert that always fails, so a case
// whose call returns panics with "assert" and a case whose call panics
// keeps its own panic. That proves every const was evaluated.
const diffAfter = `
// Appended by internal/corpus (TestDiffEvalNative): panics with assert once the case has returned.
DiffNever(): bool {
    return false
}

DiffAfter[T](v: T): T {
    Assert(DiffNever())
    return v
}
`

// EvalCheckSource returns eval.mk's source with one const per case
// appended, `const DiffCase<Name>: T = Case<Name>()`, so the driver's
// evaluator runs every case while it checks the file. With after, each
// call is wrapped in DiffAfter (appended too). It also returns the 1-based
// line of each case's const.
func EvalCheckSource(src []byte, cases []EvalCase, after bool) ([]byte, map[int]EvalCase) {
	var b bytes.Buffer
	b.Write(src)
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteByte('\n')
	}
	if after {
		b.WriteString(diffAfter)
	}
	b.WriteString("\n// Appended by internal/corpus (TestDiffEvalNative): one const per case.\n")
	line := bytes.Count(b.Bytes(), []byte("\n")) + 1
	at := make(map[int]EvalCase, len(cases))
	for _, c := range cases {
		call := "Case" + c.Name + "()"
		if after {
			call = "DiffAfter(" + call + ")"
		}
		fmt.Fprintf(&b, "const DiffCase%s: %s = %s\n", c.Name, c.Type, call)
		at[line] = c
		line++
	}
	return b.Bytes(), at
}

// evalSameKind is how the native half compares a value of a scalar type of
// eval.mk with the evaluator's: by == for the integer types, bool, char and
// str, and by evalSameFloat's rule for the floats. Any other case type must
// be a plain struct of eval.mk, compared field by field.
var evalSameKind = map[string]string{
	"i8": "eq", "i16": "eq", "i32": "eq", "i64": "eq", "int": "eq",
	"u8": "eq", "u16": "eq", "u32": "eq", "u64": "eq", "uint": "eq",
	"bool": "eq", "char": "eq", "str": "eq",
	"f32": "float", "f64": "float",
}

// evalSameFloat compares two floats as values with the same bits would be
// equal, as far as user Mk can see bits (std.raw's F64Bits is std-internal):
// both NaN, or both zero with the same sign (1 / x), or ==. A NaN's payload
// and sign are not compared.
const evalSameFloat = `
DiffSame%[1]s(a: %[2]s, b: %[2]s): bool {
    if a != a {
        return b != b
    }
    if a == 0.0 {
        return b == 0.0 && 1.0 / a == 1.0 / b
    }
    return a == b
}
`

// evalStruct matches a plain struct declaration whose fields are each on a
// line of their own (eval.mk's style).
var evalStruct = regexp.MustCompile(`(?m)^struct ([A-Z][A-Za-z0-9]*) \{\r?\n((?:    [a-z_][A-Za-z0-9_]*: [^\r\n]+\r?\n)*)\}`)

// EvalProgramSource returns eval.mk's source extended into the native
// program of TestDiffEvalNative. For every case that is
// not a Trap it appends `const DiffWant<Name>: T = Case<Name>()`, the
// evaluator's value, which codegen stores as compiled data. Its Main runs
// the case sys.args[1] names and compares the native value with that const:
// exit 0 when they are the same, 1 when not, 2 for an unknown name, and 3
// when a Trap case returned. A Trap case is only called, so its panic (exit
// 101 and the panic report) is the native result. Values compare by type:
// evalSameKind for scalars, field by field for a plain struct of eval.mk; a
// case of any other type is an error naming path:line.
//
// plant, for the liveness check, maps a case to another case whose const it
// is compared with instead; both must be non-Trap cases of one type.
func EvalProgramSource(path string, src []byte, cases []EvalCase, plant map[string]string) ([]byte, error) {
	structs := map[string][][2]string{}
	for _, m := range evalStruct.FindAllSubmatch(src, -1) {
		var fields [][2]string
		for _, l := range strings.Split(strings.TrimRight(string(m[2]), "\r\n"), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				name, typ, _ := strings.Cut(l, ": ")
				fields = append(fields, [2]string{name, strings.TrimSpace(typ)})
			}
		}
		structs[string(m[1])] = fields
	}
	var funcs bytes.Buffer
	names := map[string]string{} // DiffSame suffix -> type
	var need func(typ string, stack []string) (string, error)
	need = func(typ string, stack []string) (string, error) {
		suffix := strings.ToUpper(typ[:1]) + typ[1:]
		if prev, ok := names[suffix]; ok {
			if prev != typ {
				return "", fmt.Errorf("types %s and %s both compare with DiffSame%s", prev, typ, suffix)
			}
			return suffix, nil
		}
		if slices.Contains(stack, typ) {
			return "", fmt.Errorf("struct %s contains itself", typ)
		}
		fields, isStruct := structs[typ]
		switch kind := evalSameKind[typ]; {
		case kind == "eq":
			fmt.Fprintf(&funcs, "\nDiffSame%[1]s(a: %[2]s, b: %[2]s): bool {\n    return a == b\n}\n", suffix, typ)
		case kind == "float":
			fmt.Fprintf(&funcs, evalSameFloat, suffix, typ)
		case isStruct:
			var parts []string
			for _, f := range fields {
				fs, err := need(f[1], append(stack, typ))
				if err != nil {
					return "", err
				}
				parts = append(parts, fmt.Sprintf("DiffSame%s(a.%s, b.%s)", fs, f[0], f[0]))
			}
			if len(parts) == 0 {
				parts = []string{"true"}
			}
			fmt.Fprintf(&funcs, "\nDiffSame%[1]s(a: %[2]s, b: %[2]s): bool {\n    return %[3]s\n}\n", suffix, typ, strings.Join(parts, " && "))
		default:
			return "", fmt.Errorf("type %s is not a scalar, str or a plain struct of the file", typ)
		}
		names[suffix] = typ
		return suffix, nil
	}
	var b, main bytes.Buffer
	b.Write(src)
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n// Appended by internal/corpus (TestDiffEvalNative): the evaluator's value of every case that is not a Trap.\n")
	for k := range plant {
		if !slices.ContainsFunc(cases, func(c EvalCase) bool { return c.Name == k && !c.Trap() }) {
			return nil, fmt.Errorf("%s: planted case %q is not a non-Trap case", path, k)
		}
	}
	main.WriteString("\n// Appended by internal/corpus (TestDiffEvalNative): runs the case sys.args[1] names. Exit 0: the native value is the\n// evaluator's; 1: it is not; 2: no such case; 3: a Trap case returned.\nexport Main(sys: &System): int {\n    name := sys.args[1]\n")
	for _, c := range cases {
		suffix, err := need(c.Type, nil)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: Case%s: %v", path, c.Line, c.Name, err)
		}
		fmt.Fprintf(&main, "    if name == %q {\n", c.Name)
		if c.Trap() {
			fmt.Fprintf(&main, "        got := Case%s()\n        if DiffSame%s(got, got) {\n            return 3\n        }\n        return 3\n    }\n", c.Name, suffix)
			continue
		}
		want := c.Name
		if p, ok := plant[c.Name]; ok {
			i := slices.IndexFunc(cases, func(o EvalCase) bool { return o.Name == p })
			if i < 0 || cases[i].Trap() || cases[i].Type != c.Type {
				return nil, fmt.Errorf("%s:%d: Case%s: planted %q is not a non-Trap case of type %s", path, c.Line, c.Name, p, c.Type)
			}
			want = p
		}
		fmt.Fprintf(&b, "const DiffWant%s: %s = Case%s()\n", c.Name, c.Type, c.Name)
		fmt.Fprintf(&main, "        if DiffSame%s(Case%s(), DiffWant%s) {\n            return 0\n        }\n        return 1\n    }\n", suffix, c.Name, want)
	}
	main.WriteString("    return 2\n}\n")
	b.Write(funcs.Bytes())
	b.Write(main.Bytes())
	return b.Bytes(), nil
}

// LayoutProgramSource returns layout.mk's source extended into the native
// program of TestDiffLayout: its Main compares every row's
// Layout<Name>() with the golden's size and alignment, in row order, and
// exits with the 1-based number of the first row that differs, or 0. Exit
// statuses stop at 255 and 101 is a panic, so more than 100 rows is an
// error.
func LayoutProgramSource(src []byte, rows []LayoutRow) ([]byte, error) {
	if len(rows) > 100 {
		return nil, fmt.Errorf("%d rows: the native check numbers rows by exit status, at most 100", len(rows))
	}
	var b bytes.Buffer
	b.Write(src)
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(`
// Appended by internal/corpus (TestDiffLayout): exits with the number of the first golden row that differs, or 0.
DiffRowDiffers(l: Layout, size: int, align: int): bool {
    return l.size != size || l.align != align
}

export Main(sys: &System): int {
`)
	for i, r := range rows {
		fmt.Fprintf(&b, "    if DiffRowDiffers(Layout%s(), %d, %d) {\n        return %d\n    }\n", r.Name, r.Size, r.Align, i+1)
	}
	b.WriteString("    return 0\n}\n")
	return b.Bytes(), nil
}
