// Why: an enum's !range metadata must be a form LLVM accepts and the verifier
// must be on, or the smoke tests prove nothing.
// What: TestRangePairs (the normalization alone), TestEnumRangeMetadata and
// TestLLVMVerifierOn.
// How: the emitted !range must pass host and WSL clang 21 with the verifier on,
// and both clangs must reject a contiguous !range.

package llvm

import (
	"slices"
	"strings"
	"testing"
)

func span(lo, hi uint64) []uint64 {
	var s []uint64
	for v := lo; v <= hi; v++ {
		s = append(s, v)
	}
	return s
}

func cat(parts ...[]uint64) []uint64 {
	var s []uint64
	for _, p := range parts {
		s = append(s, p...)
	}
	return s
}

// rangeCases are u8 examples and the edge cases around the wrap.
var rangeCases = []struct {
	name   string
	bits   int
	discrs []uint64
	want   []int64
}{
	{"0_1_200", 8, []uint64{0, 1, 200}, []int64{-56, -55, 0, 2}},
	{"0_1_128to255", 8, cat([]uint64{0, 1}, span(128, 255)), []int64{-128, 2}},
	{"full", 8, span(0, 255), nil},
	{"120to136", 8, span(120, 136), []int64{120, -119}},
	{"empty", 8, nil, nil},
	{"bool", 8, []uint64{1, 0, 1}, []int64{0, 2}},
	{"100to127", 8, span(100, 127), []int64{100, -128}},
	{"0_120to127", 8, cat([]uint64{0}, span(120, 127)), []int64{0, 1, 120, -128}},
	{"wrap_beside_other", 8, cat(span(126, 129), []uint64{5}), nil},
	{"single_max", 8, []uint64{127}, []int64{127, -128}},
	{"single_min", 8, []uint64{128}, []int64{-128, -127}},
	{"u32_sparse", 32, []uint64{0, 2, 4}, []int64{0, 1, 2, 3, 4, 5}},
	{"u16_high", 16, []uint64{0xFFFF, 0}, []int64{-1, 1}},
	{"i1_full", 1, []uint64{0, 1}, nil},
	{"i1_one", 1, []uint64{1}, []int64{-1, 0}},
}

func TestRangePairs(t *testing.T) {
	t.Parallel()
	for _, c := range rangeCases {
		if got := RangePairs(c.bits, c.discrs); !slices.Equal(got, c.want) {
			t.Errorf("%s: RangePairs = %v, want %v", c.name, got, c.want)
		}
	}
}

// rangeModule loads a tag of each case with its !range (none when the
// pairs are nil) and returns it.
func rangeModule(tg target) *Module {
	m := NewModule("range.mk", tg.triple, tg.layout)
	for _, c := range rangeCases {
		ty := m.I(c.bits)
		f := m.AddFunction("tag."+c.name, FuncSig{Ret: ty, Params: []Type{m.Ptr()},
			FnAttrs: Attrs{}.Flag("nounwind")})
		b := f.NewBlock("b0")
		v := b.Load(ty, f.Param(0), 1, Range(m.Metadata().Range(ty, RangePairs(c.bits, c.discrs))),
			m.Metadata().NoUndef())
		b.Ret(&v)
	}
	return m
}

func TestEnumRangeMetadata(t *testing.T) {
	host := findHostClang(t)
	tg := resolve(t, host, smokeTriples[1])
	m := rangeModule(tg)
	text := m.String()
	for _, want := range []string{"!{i8 -56, i8 -55, i8 0, i8 2}", "!{i8 -128, i8 2}", "!{i8 120, i8 -119}"} {
		if !strings.Contains(text, want) {
			t.Errorf("module lacks %s", want)
		}
	}
	if out, ok := compile(host, tg, m); !ok {
		t.Error(out)
	}
	t.Run("wsl-clang", func(t *testing.T) {
		w := findWSLClang(t)
		wt := resolve(t, w, smokeTriples[1])
		if out, ok := compile(w, wt, rangeModule(wt)); !ok {
			t.Error(out)
		}
	})
}

// TestLLVMVerifierOn compiles a module whose !range pairs are contiguous;
// both clangs must reject it, or the smoke tests prove nothing.
func TestLLVMVerifierOn(t *testing.T) {
	bad := func(tg target) *Module {
		m := NewModule("bad.mk", tg.triple, tg.layout)
		i8 := m.I(8)
		f := m.AddFunction("f", FuncSig{Ret: i8, Params: []Type{m.Ptr()}})
		b := f.NewBlock("b0")
		v := b.Load(i8, f.Param(0), 1, Range(m.Metadata().Range(i8, []int64{0, 1, 1, 2})))
		b.Ret(&v)
		return m
	}
	host := findHostClang(t)
	tg := resolve(t, host, smokeTriples[1])
	if out, ok := compile(host, tg, bad(tg)); ok || !strings.Contains(out, "contiguous") {
		t.Errorf("clang accepted a contiguous !range: ok=%v %s", ok, out)
	}
	t.Run("wsl-clang", func(t *testing.T) {
		w := findWSLClang(t)
		wt := resolve(t, w, smokeTriples[1])
		if out, ok := compile(w, wt, bad(wt)); ok || !strings.Contains(out, "contiguous") {
			t.Errorf("WSL clang accepted a contiguous !range: ok=%v %s", ok, out)
		}
	})
}
