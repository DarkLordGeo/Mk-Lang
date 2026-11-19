// Why: an intrinsic must be declared once per module with its exact LLVM
// signature, whatever order codegen calls it in.
//
// What: Intrinsic and the table of the generic intrinsics codegen lowers
// to: checked arithmetic, memory, float and bit operations.
//
// How: each table entry builds the signature from the overload types; the
// module declares llvm.<name> on first use. No lifetime intrinsics and no
// llvm.stacksave are emitted.

package llvm

import (
	"sort"
	"strconv"
	"strings"
)

// intrinsicSig is the shape of one intrinsic family: how many overload
// types it takes and its signature over them.
type intrinsicSig struct {
	overloads int
	sig       func(m *Module, o []Type) (ret Type, params []Type, immarg []bool)
}

func unary(m *Module, o []Type) (Type, []Type, []bool) { return o[0], []Type{o[0]}, nil }
func binary(m *Module, o []Type) (Type, []Type, []bool) {
	return o[0], []Type{o[0], o[0]}, nil
}
func ternary(m *Module, o []Type) (Type, []Type, []bool) {
	return o[0], []Type{o[0], o[0], o[0]}, nil
}

// overflow is { T, i1 } (T, T), or { <N x iM>, <N x i1> } for a vector T
// (the overflow flag is per lane).
func overflow(m *Module, o []Type) (Type, []Type, []bool) {
	flag := m.I(1)
	if o[0].IsVector() {
		flag = m.Vector(int(o[0].Len()), flag)
	}
	return m.Struct(false, o[0], flag), []Type{o[0], o[0]}, nil
}
func withFlag(m *Module, o []Type) (Type, []Type, []bool) { // ctlz, cttz, abs
	return o[0], []Type{o[0], m.I(1)}, []bool{false, true}
}

var intrinsics = map[string]intrinsicSig{
	"sadd.with.overflow": {1, overflow},
	"uadd.with.overflow": {1, overflow},
	"ssub.with.overflow": {1, overflow},
	"usub.with.overflow": {1, overflow},
	"smul.with.overflow": {1, overflow},
	"umul.with.overflow": {1, overflow},
	"sadd.sat":           {1, binary},
	"uadd.sat":           {1, binary},
	"ssub.sat":           {1, binary},
	"usub.sat":           {1, binary},
	"smax":               {1, binary},
	"smin":               {1, binary},
	"umax":               {1, binary},
	"umin":               {1, binary},
	"fshl":               {1, ternary},
	"fshr":               {1, ternary},
	"ctpop":              {1, unary},
	"bswap":              {1, unary},
	"bitreverse":         {1, unary},
	"ctlz":               {1, withFlag},
	"cttz":               {1, withFlag},
	"abs":                {1, withFlag},
	"expect":             {1, binary},
	"fabs":               {1, unary},
	"sqrt":               {1, unary},
	"floor":              {1, unary},
	"ceil":               {1, unary},
	"trunc":              {1, unary},
	"rint":               {1, unary},
	"nearbyint":          {1, unary},
	"round":              {1, unary},
	"roundeven":          {1, unary},
	"copysign":           {1, binary},
	"minnum":             {1, binary},
	"maxnum":             {1, binary},
	"minimum":            {1, binary},
	"maximum":            {1, binary},
	"fma":                {1, ternary},
	"memcpy": {3, func(m *Module, o []Type) (Type, []Type, []bool) { // memcpy.p0.p0.i64
		return m.Void(), []Type{o[0], o[1], o[2], m.I(1)}, []bool{false, false, false, true}
	}},
	"memmove": {3, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.Void(), []Type{o[0], o[1], o[2], m.I(1)}, []bool{false, false, false, true}
	}},
	"memset": {2, func(m *Module, o []Type) (Type, []Type, []bool) { // memset.p0.i64
		return m.Void(), []Type{o[0], m.I(8), o[1], m.I(1)}, []bool{false, false, false, true}
	}},
	"trap": {0, func(m *Module, o []Type) (Type, []Type, []bool) { return m.Void(), nil, nil }},
	"debugtrap": {0, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.Void(), nil, nil
	}},
	"assume": {0, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.Void(), []Type{m.I(1)}, nil
	}},
}

// IntrinsicNames is the sorted list of intrinsic families Intrinsic knows.
func IntrinsicNames() []string {
	names := make([]string, 0, len(intrinsics))
	for n := range intrinsics {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IntrinsicFn declares llvm.<name>.<overloads> once per module and returns
// it; name is the family without the llvm. prefix and without the overload
// suffix ("sadd.with.overflow", "memcpy"), and overload lists the overloaded
// types in LLVM's order (memcpy: ptr, ptr, i64). Its Type is what Call takes.
// immarg parameters (the i1 of memcpy, ctlz, abs) carry immarg, as clang
// declares them.
func (m *Module) IntrinsicFn(name string, overload ...Type) *Function {
	name = strings.TrimPrefix(name, "llvm.")
	is, ok := intrinsics[name]
	if !ok {
		panic("llvm: unknown intrinsic llvm." + name)
	}
	if len(overload) != is.overloads {
		panic("llvm: intrinsic llvm." + name + " takes " + strconv.Itoa(is.overloads) + " overload types")
	}
	full := "llvm." + name
	for _, t := range overload {
		m.checkType(t, "Intrinsic")
		full += "." + mangle(t)
	}
	if f, ok := m.decls[full]; ok {
		return f
	}
	ret, params, immarg := is.sig(m, overload)
	var pa []Attrs
	if immarg != nil {
		pa = make([]Attrs, len(params))
		for i, im := range immarg {
			if im {
				pa[i] = pa[i].Flag("immarg")
			}
		}
	}
	return m.Declare(full, FuncSig{Ret: ret, Params: params, ParamAttrs: pa})
}

// Intrinsic is the address of IntrinsicFn(name, overload...), declared once
// per module: llvm.sadd.with.overflow.i32, llvm.memcpy.p0.p0.i64. The
// callee's type for Call is IntrinsicFn(...).Type().
func (m *Module) Intrinsic(name string, overload ...Type) Value {
	return m.IntrinsicFn(name, overload...).Ref()
}
