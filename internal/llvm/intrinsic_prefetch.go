// Why: the prefetch and cycle-counter intrinsics need their LLVM
// signatures in the intrinsic table.
//
// What: the entries for llvm.prefetch and llvm.readcyclecounter. The
// overflow, saturating and float families are intrinsic.go's and vector.go's.
//
// How: prefetch's rw, locality and cache-type operands are immediates.

package llvm

func init() {
	// prefetch.p0(ptr, i32 rw, i32 locality, i32 cache type): rw, locality
	// and the cache type are immediates.
	intrinsics["prefetch"] = intrinsicSig{1, func(m *Module, o []Type) (Type, []Type, []bool) {
		i32 := m.I(32)
		return m.Void(), []Type{o[0], i32, i32, i32}, []bool{false, true, true, true}
	}}
	// readcyclecounter(): i64, rdtsc on x86_64.
	intrinsics["readcyclecounter"] = intrinsicSig{0, func(m *Module, o []Type) (Type, []Type, []bool) {
		return m.I(64), nil, nil
	}}
}
