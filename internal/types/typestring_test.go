// Why: type strings, keys, niches and shape terms feed caches and diagnostics,
// so they must be exact and independent of intern order.
// What: TestTypeStringArrayLen, TestTypeStringRoundTripsPrecedence,
// TestKeysAreInjective, TestNicheOptionalIsPointerWord,
// TestShapeTermCarriesDiscrsAndMarks and the concurrent intern test and bench.
// How: builds types in different orders and from many goroutines (-race).

package types

import (
	"slices"
	"sync"
	"testing"
)

// intConst interns the int value n of type ty (little-endian, width bytes).
func intConst(ct *ConstTable, ty TypeID, width int, n uint64) ConstID {
	b := make([]byte, width)
	StoreUint(b, width, n)
	return ct.Intern(ConstValue{Type: ty, Align: uint32(width), Bytes: b})
}

// TestTypeStringArrayLen: a CAValue length or const argument prints its
// value, and the text does not depend on what else was interned first.
func TestTypeStringArrayLen(t *testing.T) {
	spell := func(prior int) (string, string, string) {
		w := newWorld()
		for i := 0; i < prior; i++ {
			intConst(w.consts, I64, 8, uint64(1000+i))
		}
		four := intConst(w.consts, Int, 8, 4)
		neg := intConst(w.consts, I8, 1, 0xfe)
		arr := w.tab.Array(ConstArg{Kind: CAValue, Value: four}, U8)
		box := w.item("main", "Box", KindStruct)
		named := w.tab.Named(box, []GenericArg{TypeArg(I32), ConstGenericArg(ConstArg{Kind: CAValue, Value: neg})})
		two := intConst(w.consts, Int, 8, 2)
		hi := w.consts.Intern(ConstValue{Type: w.tab.Array(ConstArg{Kind: CAValue, Value: two}, U8), Align: 1, Bytes: []byte("hi")})
		str := w.consts.Intern(ConstValue{Type: Str, Align: 8, Bytes: []byte{0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0},
			Relocs: []Reloc{{Offset: 0, Kind: RelocData, Data: hi}}})
		return w.tab.TypeString(arr, nil), w.tab.TypeString(named, nil), w.tab.ConstString(str)
	}
	a1, n1, s1 := spell(0)
	a2, n2, s2 := spell(5)
	if a1 != "[4]u8" || n1 != "Box[i32, -2]" || s1 != `"hi"` {
		t.Fatalf("got %q %q %q", a1, n1, s1)
	}
	if a1 != a2 || n1 != n2 || s1 != s2 {
		t.Fatalf("intern order changed the text: %q %q %q vs %q %q %q", a1, n1, s1, a2, n2, s2)
	}
	w := newWorld()
	rec := w.consts.Intern(ConstValue{Type: w.tab.Record(nil), Align: 1, Bytes: []byte{7}})
	if got := w.tab.ConstString(rec); got != "(){align 1 0x07}" {
		t.Fatalf("structural form %q", got)
	}
	bare := NewTable(w.in, w.items, nil)
	if !isICE(func() { bare.TypeString(bare.Array(ConstArg{Kind: CAValue, Value: 1}, U8), nil) }) {
		t.Fatal("spelling a value without a ConstTable must be an ICE")
	}
}

// TestTypeStringRoundTripsPrecedence: an error union under a prefix
// operator is parenthesized, since "!" binds looser than every prefix.
func TestTypeStringRoundTripsPrecedence(t *testing.T) {
	w := newWorld()
	nf := w.item("main", "NotFound", KindError)
	set := w.tab.ErrorSet([]ErrorMember{{Item: nf}})
	eu := w.tab.ErrorUnion(set, I32)
	four := intConst(w.consts, Int, 8, 4)
	dn := w.item("main", "Denied", KindError)
	two := w.tab.ErrorSetType(w.tab.ErrorSet([]ErrorMember{{Item: nf}, {Item: dn}}))
	cases := []struct {
		id   TypeID
		want string
	}{
		{w.tab.Optional(eu), "?(NotFound!i32)"},
		{w.tab.Ref(eu), "&(NotFound!i32)"},
		{w.tab.Mut(eu), "mut (NotFound!i32)"},
		{w.tab.Slice(eu), "[](NotFound!i32)"},
		{w.tab.MutSlice(eu), "mut [](NotFound!i32)"},
		{w.tab.RawPtr(eu), "*(NotFound!i32)"},
		{w.tab.Array(ConstArg{Kind: CAValue, Value: four}, eu), "[4](NotFound!i32)"},
		{w.tab.ErrorUnion(set, w.tab.Optional(I32)), "NotFound!?i32"},
		{w.tab.Optional(w.tab.Ref(I32)), "?&i32"},
		// An error-set value of several errors under a prefix.
		{w.tab.Optional(two), "?(Denied|NotFound)"},
		{w.tab.Ref(two), "&(Denied|NotFound)"},
		{w.tab.Slice(two), "[](Denied|NotFound)"},
		{w.tab.Array(ConstArg{Kind: CAValue, Value: four}, two), "[4](Denied|NotFound)"},
		{w.tab.Optional(w.tab.ErrorSetType(set)), "?NotFound"},
		{two, "Denied|NotFound"},
	}
	for _, c := range cases {
		if got := w.tab.TypeString(c.id, nil); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// TestKeysAreInjective: a member key never equals an item key.
func TestKeysAreInjective(t *testing.T) {
	if MemberKey("geo", "Point", "Add") == ItemKey("geo.Point", "Add") {
		t.Fatal("method geo.Point.Add collides with item Add of module geo.Point")
	}
	if got := MemberKey("std.fs", "File", "Close").String(); got != "std.fs.File:Close" {
		t.Fatalf("member key %q", got)
	}
	if LiteralKey(ItemKey("m", "X"), 0) == MemberKey("m", "X", "fn0") {
		t.Fatal("literal key collides with a member key")
	}
	w := newWorld()
	m := w.items.AddModule(&Module{Path: "geo"})
	mp := w.items.AddModule(&Module{Path: "geo.Point"})
	w.items.AddItem(&Item{Module: m, Name: w.in.Intern("Add"), Kind: KindFunc, Key: MemberKey("geo", "Point", "Add")})
	if isICE(func() {
		w.items.AddItem(&Item{Module: mp, Name: w.in.Intern("Add"), Kind: KindFunc, Key: ItemKey("geo.Point", "Add")})
	}) {
		t.Fatal("valid program raised a duplicate-key ICE")
	}
}

// TestNicheOptionalIsPointerWord: only ?&T, ?mut T and ?fn are one
// pointer word; ?str and ?[]T carry a some byte like every other ?T.
func TestNicheOptionalIsPointerWord(t *testing.T) {
	for k := KNone; k < numTypeKinds; k++ {
		want := k == KRef || k == KMut || k == KFn
		if NicheOptional(k) != want {
			t.Errorf("NicheOptional(%v) = %v", k, !want)
		}
	}
}

// TestShapeTermCarriesDiscrsAndMarks: the terms of enum E[T] { A(x: T)
// = 300, B } and enum F[T] { A(x: T), B } differ and give E a 2-byte tag, and
// a struct term inlined from Cell[T] is marked interior.
func TestShapeTermCarriesDiscrsAndMarks(t *testing.T) {
	param := ShapeTerm{Kind: TermParam}
	e := ShapeTerm{Kind: TermEnum, Variants: [][]ShapeTerm{{param}, nil}, Discrs: []uint64{300, 301}}
	f := ShapeTerm{Kind: TermEnum, Variants: [][]ShapeTerm{{param}, nil}, Discrs: []uint64{0, 1}}
	if slices.Equal(e.Discrs, f.Discrs) {
		t.Fatal("explicit discriminants must be visible in the term")
	}
	if EnumTagBytes(e.Discrs) != 2 || EnumTagBytes(f.Discrs) != 1 || EnumTagBytes([]uint64{70000}) != 4 ||
		EnumTagBytes(nil) != 1 || EnumTagBytes([]uint64{1 << 33}) != 0 {
		t.Fatal("tag widths of architecture 4.3")
	}
	cell := ShapeTerm{Kind: TermStruct, Fields: []ShapeTerm{param}, Marks: MarkInterior}
	w := ShapeTerm{Kind: TermStruct, Fields: []ShapeTerm{cell}}
	plain := ShapeTerm{Kind: TermStruct, Fields: []ShapeTerm{{Kind: TermStruct, Fields: []ShapeTerm{param}}}}
	if w.Fields[0].Marks&MarkInterior == 0 || plain.Fields[0].Marks != 0 {
		t.Fatal("struct W[T] { c: Cell[T] } must differ from a plain wrapper")
	}
	if MarkInterior&(MarkNoFreeze|MarkNoShare|MarkNoSend) != 0 || MarkNoFreeze == MarkNoShare {
		t.Fatal("marks are distinct bits")
	}
}

// TestTableInternConcurrentShards, under -race: many goroutines intern
// overlapping new and existing types and error sets and all agree.
func TestTableInternConcurrentShards(t *testing.T) {
	w := newWorld()
	nf := w.item("main", "NotFound", KindError)
	de := w.item("main", "Denied", KindError)
	build := func() []TypeID {
		var out []TypeID
		e := w.tab.ErrorSet([]ErrorMember{{Item: de}, {Item: nf}})
		for _, base := range []TypeID{I8, I16, I32, I64, U8, Str, Bool} {
			out = append(out, w.tab.Ref(w.tab.Slice(base)), w.tab.Optional(w.tab.Mut(base)), w.tab.ErrorUnion(e, base))
		}
		return out
	}
	results := make([][]TypeID, 16)
	var wg sync.WaitGroup
	for g := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[g] = build() }()
	}
	wg.Wait()
	for _, r := range results[1:] {
		if !slices.Equal(r, results[0]) {
			t.Fatal("concurrent interning disagreed")
		}
	}
}

// BenchmarkTableInternExistingParallel: lookups of existing types
// from many goroutines. Run with -cpu 1,8.
func BenchmarkTableInternExistingParallel(b *testing.B) {
	w := newWorld()
	elems := []TypeID{I8, I16, I32, I64, U8, U16, U32, U64, Int, Uint, F32, F64, Bool, Char, Str}
	for _, e := range elems {
		w.tab.Ref(w.tab.Slice(e))
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			w.tab.Ref(w.tab.Slice(elems[i%len(elems)]))
			i++
		}
	})
}
