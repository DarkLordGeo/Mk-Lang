// Why: types and constants must print deterministically and float constants
// exactly.
// What: TestLLVMFloatHex, TestLLVMTypes, TestLLVMConstants,
// TestLLVMDeterministic and TestLLVMMisuseIsICE.
// How: compares printed forms; floats print in exact hex.

package llvm

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestLLVMFloatHex(t *testing.T) {
	t.Parallel()
	m := NewModule("t", "", "")
	f32 := []struct {
		bits uint32
		want string
	}{
		{math.Float32bits(0.1), "float 0x3FB99999A0000000"},
		{math.Float32bits(1), "float 0x3FF0000000000000"},
		{0x7FC00000, "float 0x7FF8000000000000"}, // canonical quiet NaN
		{0xFFC00001, "float 0xFFF8000020000000"}, // negative NaN with payload
		{0x7F800001, "float 0x7FF0000020000000"}, // signalling NaN keeps its bits
		{0x7F800000, "float 0x7FF0000000000000"}, // +inf
		{0xFF800000, "float 0xFFF0000000000000"}, // -inf
		{0x80000000, "float 0x8000000000000000"}, // -0
		{0x00000000, "float 0x0000000000000000"}, // +0
		{0x00000001, "float 0x36A0000000000000"}, // smallest subnormal, 2^-149
		{0x007FFFFF, "float 0x380FFFFFC0000000"}, // largest subnormal
		{math.Float32bits(math.MaxFloat32), "float 0x47EFFFFFE0000000"},
	}
	for _, c := range f32 {
		if got := m.ConstF32(c.bits).String(); got != c.want {
			t.Errorf("ConstF32(%#08x) = %s, want %s", c.bits, got, c.want)
		}
		// Every non-NaN widening must equal the FPU's, which is exact.
		if f := math.Float32frombits(c.bits); f == f {
			if w := f32ToF64Bits(c.bits); w != math.Float64bits(float64(f)) {
				t.Errorf("f32ToF64Bits(%#08x) = %#016x, FPU %#016x", c.bits, w, math.Float64bits(float64(f)))
			}
		}
	}
	f64 := []struct {
		bits uint64
		want string
	}{
		{math.Float64bits(0.1), "double 0x3FB999999999999A"},
		{0x7FF8000000000000, "double 0x7FF8000000000000"},
		{math.Float64bits(math.Inf(-1)), "double 0xFFF0000000000000"},
		{math.Float64bits(math.Copysign(0, -1)), "double 0x8000000000000000"},
	}
	for _, c := range f64 {
		if got := m.ConstF64(c.bits).String(); got != c.want {
			t.Errorf("ConstF64(%#016x) = %s, want %s", c.bits, got, c.want)
		}
	}
	// Every single-precision exponent: the widening agrees with the FPU.
	for e := uint32(0); e < 255; e++ {
		for _, man := range []uint32{0, 1, 0x400000, 0x7FFFFF} {
			b := e<<23 | man
			if w, want := f32ToF64Bits(b), math.Float64bits(float64(math.Float32frombits(b))); w != want {
				t.Fatalf("f32ToF64Bits(%#08x) = %#016x, want %#016x", b, w, want)
			}
		}
	}
}

func TestLLVMTypes(t *testing.T) {
	t.Parallel()
	m := NewModule("t", "", "")
	i8, i64, p := m.I(8), m.I(64), m.Ptr()
	cases := []struct {
		ty   Type
		want string
	}{
		{m.I(1), "i1"}, {m.I(128), "i128"}, {p, "ptr"}, {m.Float(), "float"}, {m.Double(), "double"},
		{m.Void(), "void"}, {m.Array(16, i8), "[16 x i8]"}, {m.Array(0, i8), "[0 x i8]"},
		{m.Struct(false, i64, m.Double()), "{ i64, double }"}, {m.Struct(true, i8, p), "<{ i8, ptr }>"},
		{m.Struct(false), "{}"}, {m.Struct(true), "<{}>"}, {m.Vector(2, m.Float()), "<2 x float>"},
		{m.FuncType(m.Void(), nil, false), "void ()"}, {m.FuncType(m.I(32), []Type{p}, true), "i32 (ptr, ...)"},
		{m.FuncType(i64, []Type{i64, p}, false), "i64 (i64, ptr)"}, {m.FuncType(m.Void(), nil, true), "void (...)"},
	}
	for _, c := range cases {
		if got := c.ty.String(); got != c.want {
			t.Errorf("type = %s, want %s", got, c.want)
		}
	}
	if m.Struct(false, i64, m.Double()) != m.Struct(false, m.I(64), m.Double()) {
		t.Error("equal struct types are not interned to one Type")
	}
	if m.Struct(true, i8) == m.Struct(false, i8) {
		t.Error("packed and unpacked structs are one Type")
	}
	if (Type{}).String() != "" || !(Type{}).IsZero() {
		t.Error("zero Type")
	}
	if got := mangle(m.Vector(4, m.I(32))); got != "v4i32" {
		t.Errorf("mangle(<4 x i32>) = %s", got)
	}
}

func TestLLVMConstants(t *testing.T) {
	t.Parallel()
	m := NewModule("t", "", "")
	i8, i32, i64 := m.I(8), m.I(32), m.I(64)
	g := m.AddGlobal("g", GlobalAttrs{Type: i64, Init: m.ConstInt(i64, 0)})
	cases := []struct {
		v    Value
		want string
	}{
		{m.ConstInt(m.I(1), 1), "i1 true"}, {m.ConstInt(m.I(1), 0), "i1 false"},
		{m.ConstInt(i8, 200), "i8 -56"}, {m.ConstInt(i8, 0x1FF), "i8 -1"}, {m.ConstInt(i32, 7), "i32 7"},
		{m.ConstInt(i64, math.MaxUint64), "i64 -1"}, {m.ConstInt(m.I(128), math.MaxUint64), "i128 -1"},
		{m.ConstInt(m.I(16), 0x8000), "i16 -32768"}, {m.Null(), "ptr null"}, {m.Undef(i32), "i32 undef"},
		{m.Poison(i32), "i32 poison"}, {m.ZeroInit(i32), "i32 0"}, {m.ZeroInit(m.Ptr()), "ptr null"},
		{m.ZeroInit(m.Double()), "double 0x0000000000000000"},
		{m.ZeroInit(m.Array(3, i8)), "[3 x i8] zeroinitializer"},
		{m.ConstStruct(true, m.Bytes([]byte{1, 2}), m.GlobalRef(g)), `<{ [2 x i8], ptr }> <{ [2 x i8] c"\01\02", ptr @g }>`},
		{m.ConstStruct(false), "{} {}"},
		{m.ConstArray(i32, m.ConstInt(i32, 1), m.ConstInt(i32, 2)), "[2 x i32] [i32 1, i32 2]"},
		{m.ConstArray(i32), "[0 x i32] zeroinitializer"},
		{m.ConstGEP(m.GlobalRef(g), 8), "ptr getelementptr inbounds (i8, ptr @g, i64 8)"},
		{m.ConstIntToPtr(8), "ptr inttoptr (i64 8 to ptr)"},
	}
	for _, c := range cases {
		if got := c.v.String(); got != c.want {
			t.Errorf("constant = %s, want %s", got, c.want)
		}
	}
}

// TestLLVMDeterministic builds one module twice and requires equal bytes.
func TestLLVMDeterministic(t *testing.T) {
	t.Parallel()
	var outs [2][]byte
	for i := range outs {
		var buf bytes.Buffer
		if _, err := buildEvery(goldenTarget).WriteTo(&buf); err != nil {
			t.Fatal(err)
		}
		outs[i] = buf.Bytes()
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two builds of one module print different bytes")
	}
}

// TestLLVMMisuseIsICE checks that builder misuse panics with an "llvm: "
// message (an ICE for the caller's Guard) and that WriteTo refuses an
// unterminated block.
func TestLLVMMisuseIsICE(t *testing.T) {
	t.Parallel()
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			r := recover()
			s, ok := r.(string)
			if !ok || !strings.HasPrefix(s, "llvm: ") {
				t.Errorf("%s: recovered %v, want an llvm: panic", name, r)
			}
		}()
		fn()
	}
	m := NewModule("t", "", "")
	i32 := m.I(32)
	f := m.AddFunction("f", FuncSig{Ret: i32, Params: []Type{i32}})
	b := f.NewBlock("b0")
	mustPanic("duplicate symbol", func() { m.AddFunction("f", FuncSig{Ret: i32}) })
	mustPanic("duplicate label", func() { f.NewBlock("b0") })
	mustPanic("reserved label", func() { f.NewBlock("v3") })
	mustPanic("type mismatch", func() { b.Bin(Add, f.Param(0), m.ConstInt(m.I(64), 1), 0) })
	mustPanic("flag", func() { b.Bin(And, f.Param(0), f.Param(0), NSW) })
	mustPanic("ret type", func() { b.Ret(nil) })
	mustPanic("capture attr", func() {
		m.Declare("g", FuncSig{Ret: i32, Params: []Type{m.Ptr()}, ParamAttrs: []Attrs{Attrs{}.Flag("nocapture")}})
	})
	mustPanic("foreign type", func() { NewModule("u", "", "").Array(2, i32) })
	mustPanic("redeclare", func() {
		m.Declare("h", FuncSig{Ret: i32})
		m.Declare("h", FuncSig{Ret: m.I(64)})
	})
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err == nil || buf.Len() != 0 {
		t.Errorf("WriteTo of an unterminated block: err %v, %d bytes", err, buf.Len())
	}
	v := f.Param(0)
	b.Ret(&v)
	mustPanic("after terminator", func() { b.Unreachable() })
	if _, err := m.WriteTo(&buf); err != nil {
		t.Errorf("WriteTo: %v", err)
	}
}
