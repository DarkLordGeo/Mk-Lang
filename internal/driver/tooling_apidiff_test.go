// Why: mk apidiff decides whether a new version of a package breaks its
// users, so each kind of change must get the right severity.
// What: TestAPIDiffSeverities over the whole severity table, and tests of
// incomplete roots, interface methods, error payloads, enum methods and
// variants, removed impls and struct fields over open error sets.
// How: writes two versions of one package and compares mk apidiff's
// changes by line and severity.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

const apiV1 = `export struct P {
    export x: int
}

export const Limit: int = 10

export F(a: int): int {
    return a
}

export G(a: &P, b: &P): &P from a {
    return a
}

export Gone() {
}

export H(io: Io): OutOfMemory!void {
}
`

const apiV2 = `export struct P {
    export x: int
    y: i64
}

export const Limit: int = 11

export F(a: int, b: int): int {
    return a + b
}

export G(a: &P, b: &P): &P from b {
    return b
}

export New() {
}

export H(io: Io): OutOfMemory!void {
}
`

// TestAPIDiffSeverities: a changed signature and flow and a removed item
// are breaking, a size change is recompile, a const value is reported, an
// added item is compatible, and an unchanged item is not listed.
func TestAPIDiffSeverities(t *testing.T) {
	a := testPkg(t, map[string]string{"p.mk": apiV1})
	b := testPkg(t, map[string]string{"p.mk": apiV2})
	res, err := APIDiff(Config{Root: filepath.Join(a, "p.mk"), CacheDir: t.TempDir()}, Config{Root: filepath.Join(b, "p.mk"), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Text()
	for _, want := range []string{
		"breaking  main.F  signature: export F(a: int): int  ->  export F(a: int, b: int): int",
		"breaking  main.G  signature: ",
		"from b",
		"breaking  main.Gone  removed",
		"compatible  main.New  added",
		"recompile  main.P  size: 8 align 8  ->  16 align 8",
		"reported  main.Limit  value: 10  ->  11",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "main.H ") || res.ExitStatus() != 1 {
		t.Errorf("exit %d:\n%s", res.ExitStatus(), text)
	}
	same, err := APIDiff(Config{Root: filepath.Join(a, "p.mk"), CacheDir: t.TempDir()}, Config{Root: filepath.Join(a, "p.mk"), CacheDir: t.TempDir()})
	if err != nil || len(same.Changes) != 0 || same.ExitStatus() != 0 {
		t.Errorf("same root: %v %s", err, same.Text())
	}
}

// TestAPIDiffIncompleteRoot: a root that does not check as far as types (a
// directory program with no Main is a resolve error, a parse error stops
// earlier) is not compared: half-checked signatures would make a changed
// signature look unchanged. The result is Incomplete, with no change, exit 1.
func TestAPIDiffIncompleteRoot(t *testing.T) {
	a := testPkg(t, map[string]string{"p.mk": apiV1})
	b := testPkg(t, map[string]string{"p.mk": apiV2})
	bad := testPkg(t, map[string]string{"p.mk": "export F(a: int): int {\n    return a +\n"})
	for _, c := range []struct{ name, a, b string }{
		{"directories without Main", a, b},
		{"parse error", filepath.Join(a, "p.mk"), filepath.Join(bad, "p.mk")},
	} {
		res, err := APIDiff(Config{Root: c.a, CacheDir: t.TempDir()}, Config{Root: c.b, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !res.Incomplete || len(res.Changes) != 0 || res.ExitStatus() != 1 || !strings.Contains(res.Text(), "not compared") {
			t.Errorf("%s: incomplete %v, exit %d:\n%s", c.name, res.Incomplete, res.ExitStatus(), res.Text())
		}
	}
}

// apiDiffFiles diffs two single-file roots p.mk.
func apiDiffFiles(t *testing.T, v1, v2 string) *APIDiffResult {
	t.Helper()
	a := testPkg(t, map[string]string{"p.mk": v1})
	b := testPkg(t, map[string]string{"p.mk": v2})
	res, err := APIDiff(Config{Root: filepath.Join(a, "p.mk"), CacheDir: t.TempDir()}, Config{Root: filepath.Join(b, "p.mk"), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Incomplete {
		t.Fatalf("not compared:\n%s\n%s", res.A.Report.Text(res.A.Sources), res.B.Report.Text(res.B.Sources))
	}
	return res
}

const apiIface = `export interface Shape {
    Area(&self): f64
}
`

// TestAPIDiffInterfaceMethods: a method added to an exported
// interface, or a method's result type changed, breaks every implementer
// outside the module, so it is a breaking change on the interface.
func TestAPIDiffInterfaceMethods(t *testing.T) {
	for _, c := range []struct{ name, v2, want string }{
		{"added", "export interface Shape {\n    Area(&self): f64\n    Perimeter(&self): f64\n}\n", "Perimeter(&self): f64"},
		{"result", "export interface Shape {\n    Area(&self): i64\n}\n", "Area(&self): i64"},
	} {
		res := apiDiffFiles(t, apiIface, c.v2)
		text := res.Text()
		if !strings.Contains(text, "breaking  main.Shape  methods: export Area(&self): f64  ->  ") || !strings.Contains(text, c.want) || res.ExitStatus() != 1 {
			t.Errorf("%s: exit %d:\n%s", c.name, res.ExitStatus(), text)
		}
	}
}

// TestAPIDiffErrorPayload: a changed payload of an exported error
// breaks every match on it.
func TestAPIDiffErrorPayload(t *testing.T) {
	res := apiDiffFiles(t, "export error NotFound(id: i64)\n", "export error NotFound(id: i64, name: i32)\n")
	text := res.Text()
	if !strings.Contains(text, "breaking  main.NotFound  fields: export id: i64  ->  export id: i64; export name: i32") || res.ExitStatus() != 1 {
		t.Errorf("exit %d:\n%s", res.ExitStatus(), text)
	}
}

const apiEnumV1 = "export enum Color {\n    Red\n    Blue\n}\n"

// TestAPIDiffEnumMethodAdded: an enum's "fields" fact is its variants only,
// so a method added to an exported enum is one compatible addition (the
// methods are not listed after the variants), while a variant added still
// breaks every match.
func TestAPIDiffEnumMethodAdded(t *testing.T) {
	method := apiEnumV1 + "\nexport Color.Warm(self): bool {\n    return match self {\n        .Red { true }\n        .Blue { false }\n    }\n}\n"
	res := apiDiffFiles(t, apiEnumV1, method)
	text := res.Text()
	if !strings.Contains(text, "compatible  main.Color.Warm  added") || strings.Contains(text, "breaking") || len(res.Changes) != 1 || res.ExitStatus() != 0 {
		t.Errorf("method added: exit %d:\n%s", res.ExitStatus(), text)
	}
	variant := apiDiffFiles(t, apiEnumV1, "export enum Color {\n    Red\n    Blue\n    Green\n}\n")
	if text := variant.Text(); !strings.Contains(text, "breaking  main.Color  fields: Red; Blue  ->  Red; Blue; Green") || variant.ExitStatus() != 1 {
		t.Errorf("variant added: exit %d:\n%s", variant.ExitStatus(), text)
	}

	// With the variants alone in "fields", every change to a variant still breaks,
	// a method is compared as a method, and an open enum's added variant
	// is compatible.
	open := strings.Replace(apiEnumV2, "export enum Color {", "/// May gain members.\nexport enum Color {", 1)
	for _, c := range []struct {
		name     string
		v1       string
		edits    []string // old, new pairs applied to v1
		want     []string
		exit     int
		nChanges int
	}{
		{"unchanged", apiEnumV2, nil, []string{"no API change"}, 0, 0},
		{"method added", apiEnumV2, []string{"export enum Opt[T] {", "export Color.Cool(self): bool {\n    return false\n}\n\nexport enum Opt[T] {"},
			[]string{"compatible  main.Color.Cool  added: export Color.Cool(self): bool"}, 0, 1},
		{"method removed", apiEnumV2, []string{apiEnumWarm, ""},
			[]string{"breaking  main.Color.Warm  removed: export Color.Warm(self): bool"}, 1, 1},
		{"method re-signed", apiEnumV2, []string{"export Color.Warm(self)", "export Color.Warm(&self)"},
			[]string{"breaking  main.Color.Warm  signature: export Color.Warm(self): bool  ->  export Color.Warm(&self): bool"}, 1, 1},
		{"generic enum's method removed", apiEnumV2, []string{apiEnumIsA, ""},
			[]string{"breaking  main.Opt.IsA  removed: export Opt.IsA(&self): bool"}, 1, 1},
		{"impl removed", apiEnumV2, []string{apiEnumImpl, ""},
			[]string{"breaking  main.impl Named for Color  removed: impl Named for Color"}, 1, 1},
		{"generic enum's bound added", apiEnumV2, []string{"export enum Opt[T] {", "export enum Opt[T: Named] {"},
			[]string{"breaking  main.Opt  declaration: export enum Opt[T]  (module main)  ->  export enum Opt[T: Named]  (module main)"}, 1, 1},
		{"variant added", apiEnumV2, []string{"    Blue(x: i32)\n}", "    Blue(x: i32)\n    Green\n}", "        .Blue(x: _) { false }\n", "        .Blue(x: _) { false }\n        .Green { false }\n"},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red; Blue(x: i32); Green"}, 1, 1},
		{"variants reordered", apiEnumV2, []string{"    Red\n    Blue(x: i32)\n", "    Blue(x: i32)\n    Red\n"},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Blue(x: i32); Red"}, 1, 1},
		{"payload type changed", apiEnumV2, []string{"    Blue(x: i32)\n", "    Blue(x: i64)\n"},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red; Blue(x: i64)", "recompile  main.Color  size: 8 align 4  ->  16 align 8"}, 1, 2},
		{"payload renamed", apiEnumV2, []string{"    Blue(x: i32)\n", "    Blue(y: i32)\n", ".Blue(x: _)", ".Blue(y: _)"},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red; Blue(y: i32)"}, 1, 1},
		{"variant removed", apiEnumV2, []string{"    Blue(x: i32)\n", "", "        .Blue(x: _) { false }\n", ""},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red", "recompile  main.Color  size: 8 align 4  ->  1 align 1"}, 1, 2},
		{"variant renamed", apiEnumV2, []string{"    Blue(x: i32)\n", "    Green(x: i32)\n", ".Blue(x: _)", ".Green(x: _)"},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red; Green(x: i32)"}, 1, 1},
		{"open enum's variant added", open, []string{"    Blue(x: i32)\n}", "    Blue(x: i32)\n    Green\n}", "        .Blue(x: _) { false }\n", "        .Blue(x: _) { false }\n        .Green { false }\n"},
			[]string{"compatible  main.Color  members added: Red; Blue(x: i32)  ->  Red; Blue(x: i32); Green"}, 0, 1},
		{"open enum's variant removed", open, []string{"    Blue(x: i32)\n", "", "        .Blue(x: _) { false }\n", ""},
			[]string{"breaking  main.Color  fields: Red; Blue(x: i32)  ->  Red", "recompile  main.Color  size: 8 align 4  ->  1 align 1"}, 1, 2},
	} {
		// Each row checks two packages, so the rows run in parallel.
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v2 := c.v1
			for i := 0; i+1 < len(c.edits); i += 2 {
				next := strings.Replace(v2, c.edits[i], c.edits[i+1], 1)
				if next == v2 {
					t.Fatalf("edit %d does not apply", i/2)
				}
				v2 = next
			}
			res := apiDiffFiles(t, c.v1, v2)
			text := res.Text()
			ok := res.ExitStatus() == c.exit && len(res.Changes) == c.nChanges
			for _, w := range c.want {
				ok = ok && strings.Contains(text, w)
			}
			if !ok {
				t.Errorf("exit %d, %d changes:\n%s", res.ExitStatus(), len(res.Changes), text)
			}
		})
	}
}

const apiEnumWarm = `export Color.Warm(self): bool {
    return match self {
        .Red { true }
        .Blue(x: _) { false }
    }
}

`

const apiEnumImpl = `impl Named for Color {
    Name(&self): str {
        return "c"
    }
}

`

const apiEnumIsA = `
export Opt[T].IsA(&self): bool {
    return match self {
        .A(x: _) { true }
        .B { false }
    }
}
`

// apiEnumV2 is a base version with an enum with a payload, a method
// and an impl, and a generic enum with a method.
const apiEnumV2 = `export interface Named {
    Name(&self): str
}

export enum Color {
    Red
    Blue(x: i32)
}

` + apiEnumWarm + apiEnumImpl + `export enum Opt[T] {
    A(x: T)
    B
}
` + apiEnumIsA

const apiImplV1 = `export interface Shape {
    Area(&self): f64
}

export struct Square {
    export side: f64
}

impl Shape for Square {
    Area(&self): f64 {
        return self.side * self.side
    }
}

export Larger[T: Shape](a: &T, b: &T): bool {
    return a.Area() > b.Area()
}
`

// TestAPIDiffImplRemoved: an impl is the fact a bound depends on, so
// removing impl Shape for Square breaks a caller's Larger(&sq, &sq); adding
// it back is compatible, and a changed bound of a generic impl is breaking.
func TestAPIDiffImplRemoved(t *testing.T) {
	v2 := strings.Replace(apiImplV1, "impl Shape for Square {\n    Area(&self): f64 {\n        return self.side * self.side\n    }\n}\n", "", 1)
	if v2 == apiImplV1 {
		t.Fatal("v2 still has the impl")
	}
	res := apiDiffFiles(t, apiImplV1, v2)
	if text := res.Text(); !strings.Contains(text, "breaking  main.impl Shape for Square  removed: impl Shape for Square") || res.ExitStatus() != 1 {
		t.Errorf("removed: exit %d:\n%s", res.ExitStatus(), text)
	}
	back := apiDiffFiles(t, v2, apiImplV1)
	if text := back.Text(); !strings.Contains(text, "compatible  main.impl Shape for Square  added") || back.ExitStatus() != 0 {
		t.Errorf("added: exit %d:\n%s", back.ExitStatus(), text)
	}
	const boxed = `export interface Named {
    Name(&self): int
}

export struct Box[T] {
    export v: T
}

impl[T: Named] Named for Box[T] {
    Name(&self): int {
        return self.v.Name()
    }
}
`
	bound := strings.Replace(boxed, "impl[T: Named] Named", "impl[T: Named + Plain] Named", 1)
	res = apiDiffFiles(t, boxed, bound)
	if text := res.Text(); !strings.Contains(text, "breaking  main.impl Named for Box[T]  impl: impl[T: Named] Named for Box[T]  ->  impl[T: Named + Plain] Named for Box[T]") || res.ExitStatus() != 1 {
		t.Errorf("bound: exit %d:\n%s", res.ExitStatus(), text)
	}
	if same := apiDiffFiles(t, apiImplV1, apiImplV1); len(same.Changes) != 0 {
		t.Errorf("same root:\n%s", same.Text())
	}
}

// TestAPIDiffStructFieldGrowsWithOpenSet: a struct field whose fn type
// spells an open error set out, as std.http.Route's handler spells
// http.Error, grows with that set, so a member added to the open set is a
// compatible change of the field; the same field over a set with no open
// mark is still breaking.
func TestAPIDiffStructFieldGrowsWithOpenSet(t *testing.T) {
	const v1 = "export error Bad\nexport error Late\n\n/// May gain members.\nexport type Error = Bad | Late\n\n" +
		"export struct Route {\n    export handler: fn(x: int): Error!void\n}\n"
	v2 := strings.Replace(strings.Replace(v1, "export error Late\n", "export error Late\nexport error Proxy\n", 1), "= Bad | Late", "= Bad | Late | Proxy", 1)
	res := apiDiffFiles(t, v1, v2)
	text := res.Text()
	if !strings.Contains(text, "compatible  main.Route  fields: errors added to an open set") || strings.Contains(text, "breaking") || res.ExitStatus() != 0 {
		t.Errorf("open set grew: exit %d:\n%s", res.ExitStatus(), text)
	}
	closed1 := strings.Replace(v1, "/// May gain members.\n", "", 1)
	closed2 := strings.Replace(v2, "/// May gain members.\n", "", 1)
	res = apiDiffFiles(t, closed1, closed2)
	if text := res.Text(); !strings.Contains(text, "breaking  main.Route  fields:") || res.ExitStatus() != 1 {
		t.Errorf("closed set grew: exit %d:\n%s", res.ExitStatus(), text)
	}
}
