// Why: std declares the generator API in Mk and gen implements it in Go; a
// drift between them is otherwise an E0000 at the first gen line that calls
// an unknown intrinsic, or a wrong answer from a reordered enum.
// What: TestStdSurfaceInSync: the generator API std declares
// (std/core/core_gen.mk) and the host gen implements are the same set of
// keys, and the enums gen answers by discriminant (TypeKind, Receiver) are
// declared in the order gen assumes.
// How: parses core_gen.mk's declarations and compares them with the host's
// table and method count.

package gen

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// hostedAhead are API methods gen's host implements before
// std/core/core_gen.mk declares them, each with a note on what adds the
// line. A generator cannot call one until then.
var hostedAhead = map[string]string{}

// flowMethods counts the flow API methods: Method.HasFlows, Flow's four
// queries and Gen.Input; Method.Flows changes its result, not its
// count.
const flowMethods = 6

func TestStdSurfaceInSync(t *testing.T) {
	path := filepath.Join("..", "..", "std", "core", "core_gen.mk")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sm := source.NewSourceMap()
	in := source.NewInterner()
	f, ds := syntax.ParseFile(sm.Add("std/core/core_gen.mk", text), in, syntax.Options{AllowBodiless: true})
	if len(ds) != 0 {
		t.Fatalf("core_gen.mk does not parse: %v", ds)
	}
	var declared []string
	enums := map[string][]string{}
	for _, it := range f.Items {
		switch d := it.(type) {
		case *ast.FuncDecl:
			if d.Sig == nil || d.Sig.Name == nil || d.Sig.Name.Name == nil {
				continue
			}
			if d.Body != nil {
				t.Errorf("%s has a body; every generator API method is an intrinsic gen implements", in.Str(d.Sig.Name.Name.Name))
			}
			key := "std.core." + in.Str(d.Sig.Name.Name.Name)
			if d.Sig.Name.Owner != nil {
				key = "std.core." + in.Str(d.Sig.Name.Owner.Name) + ":" + in.Str(d.Sig.Name.Name.Name)
			}
			declared = append(declared, key)
		case *ast.EnumDecl:
			for _, v := range d.Variants {
				enums[in.Str(d.Name.Name)] = append(enums[in.Str(d.Name.Name)], in.Str(v.Name.Name))
			}
		}
	}
	var hosted []string
	for k := range hostKeys {
		if k != "#quote" {
			hosted = append(hosted, k)
		}
	}
	slices.Sort(declared)
	slices.Sort(hosted)
	for _, k := range declared {
		if _, ok := hostKeys[k]; !ok {
			t.Errorf("std declares %s, which gen's host does not implement", k)
		}
	}
	for _, k := range hosted {
		if why, ok := hostedAhead[k]; ok && !slices.Contains(declared, k) {
			t.Logf("gen's host implements %s ahead of std's declaration: %s", k, why)
			continue
		}
		if !slices.Contains(declared, k) {
			t.Errorf("gen's host implements %s, which std/core/core_gen.mk does not declare", k)
		}
	}
	want := 36 + 1 + flowMethods // the 36 base methods, Field.Doc, the flow methods
	for k := range hostedAhead {
		if slices.Contains(declared, k) {
			want++
			t.Logf("%s is declared now: drop it from hostedAhead", k)
		}
	}
	if len(declared) != want {
		t.Errorf("%d API methods declared, want %d (2.3's 36, Field.Doc and 1.3's six)", len(declared), want)
	}
	// FlowKind: check's FlowFrom to FlowStore, in std's declaration order.
	if want := []string{"From", "Into", "Ends", "Store"}; !slices.Equal(enums["FlowKind"], want) {
		t.Errorf("FlowKind is %v in std, gen and check assume %v", enums["FlowKind"], want)
	}
	// TypeKind: gen's tk constants, in std's declaration order.
	kinds := []string{"Struct", "Enum", "Error", "Interface", "Record", "Scalar", "Ref", "Slice", "Array", "Optional",
		"Fallible", "Fn", "RawPtr", "Opaque"}
	if !slices.Equal(enums["TypeKind"], kinds) || tkOpaque != uint64(len(kinds)-1) || tkInterface != 3 {
		t.Errorf("TypeKind is %v in std, gen assumes %v", enums["TypeKind"], kinds)
	}
	// Receiver: gen answers uint64(types.Receiver), so std's order must be
	// types' order: NoReceiver, ByValue, ByShared, ByMut.
	if want := []string{"Static", "Value", "Ref", "Mut"}; !slices.Equal(enums["Receiver"], want) ||
		types.NoReceiver != 0 || types.ByValue != 1 || types.ByShared != 2 || types.ByMut != 3 {
		t.Errorf("Receiver is %v in std, gen assumes %v in types.Receiver's order", enums["Receiver"], want)
	}
}
