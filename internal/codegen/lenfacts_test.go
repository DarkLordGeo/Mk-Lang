// Why: LLVM can drop overflow and bounds work only with the facts that
// lengths are below 2^56 and addresses do not wrap.
// What: TestIndexAndLengthFacts: an indexed address is mul nuw nsw and
// getelementptr inbounds nuw; every slice or str length load carries !range
// [0, 2^56 / size] (only non-negative for a zero-size element).
// How: a slice value is copied as its two words with their facts, never as
// a memcpy, so a promoted local keeps them; funcIR reads each function.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// funcIR is the text of one definition of ll.
func funcIR(t *testing.T, ll, symbol string) string {
	t.Helper()
	i := strings.Index(ll, "@"+symbol+"(")
	if i < 0 {
		t.Fatalf("no definition of %s", symbol)
	}
	i = strings.LastIndex(ll[:i], "\ndefine ")
	j := strings.Index(ll[i:], "\n}\n")
	return ll[i : i+j]
}

func TestIndexAndLengthFacts(t *testing.T) {
	e := newTestEnv()
	zero := e.structT("Zero")
	cases := []struct {
		name string
		elem types.TypeID // NoType: str
		hi   string       // the !range upper bound
	}{
		{"I64", types.Int, "9007199254740993"},                             // 2^56/8 + 1
		{"Str", types.NoType, "72057594037927937"},                         // 2^56 + 1
		{"Zero", zero, "-9223372036854775808"},                             // [0, 2^63)
		{"Pair", e.structT("P", types.Int, types.I32), "4503599627370497"}, // 2^56/16 + 1
	}
	var bodies []*mir.Body
	for _, c := range cases {
		st := types.Str
		if c.elem != types.NoType {
			st = e.tt.Slice(c.elem)
		}
		// Len<c>(xs: []T): int { t := xs; return t.Len() }
		f := e.body(e.fn("Len"+c.name, []types.TypeID{st}, types.Int, 0))
		tmp := f.local(st)
		f.assign(place(tmp), use(mir.Copy(place(1))), f.at(2, 3))
		f.assign(place(0), mir.Rvalue{Kind: mir.RvLen, Place: place(tmp)}, f.at(3, 3))
		f.ret(f.at(3, 3))
		bodies = append(bodies, f.b)
	}
	// At(xs: []int, i: int): int { return xs[i] } (lower's check elided).
	fa := e.body(e.fn("At", []types.TypeID{e.tt.Slice(types.Int), types.Int}, types.Int, 0))
	fa.assign(place(0), use(mir.Copy(fa.b.Place(1, mir.Projection{Kind: mir.ProjIndex, A: 2, Type: types.Int}))), fa.at(5, 3))
	fa.ret(fa.at(5, 3))
	bodies = append(bodies, fa.b)

	ll := emit(t, e, unitOf(e, bodies), types.X86_64Linux, types.Dev)
	md := map[string]string{} // metadata id -> node
	for _, m := range regexp.MustCompile(`(?m)^(![0-9]+) = (.*)$`).FindAllStringSubmatch(ll, -1) {
		md[m[1]] = m[2]
	}
	lenLoad := regexp.MustCompile(`load i64, ptr %v[0-9]+, align 8, !range (![0-9]+), !noundef`)
	for _, c := range cases {
		fn := funcIR(t, ll, "mk.main.Len"+c.name)
		if strings.Contains(fn, "llvm.memcpy") {
			t.Errorf("Len%s copies the slice with memcpy:\n%s", c.name, fn)
		}
		if !strings.Contains(fn, "load ptr, ptr %p0, align 8, !nonnull") && !strings.Contains(fn, "!nonnull") {
			t.Errorf("Len%s: no nonnull load of the data word:\n%s", c.name, fn)
		}
		ms := lenLoad.FindAllStringSubmatch(fn, -1)
		if len(ms) < 2 { // the copy's length word and RvLen's
			t.Errorf("Len%s: %d length loads with !range, want 2:\n%s", c.name, len(ms), fn)
		}
		for _, m := range ms {
			if want := "!{i64 0, i64 " + c.hi + "}"; md[m[1]] != want {
				t.Errorf("Len%s: !range %s, want %s", c.name, md[m[1]], want)
			}
		}
	}
	at := funcIR(t, ll, "mk.main.At")
	if !regexp.MustCompile(`mul nuw nsw i64 %v[0-9]+, 8`).MatchString(at) || !strings.Contains(at, "getelementptr inbounds nuw i8") {
		t.Errorf("At: the indexed address lacks nuw nsw or inbounds nuw:\n%s", at)
	}
	for _, target := range types.Targets() {
		clangC(t, target, t.TempDir(), "lenfacts", emit(t, e, unitOf(e, bodies), target, types.Release), "-O2")
	}
}
