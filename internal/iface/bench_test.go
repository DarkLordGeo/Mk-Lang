// Why: hashing, encoding and decoding run on every build, warm or cold, so
// their cost per item must stay visible.
// What: benchmarks of type hashing, interface encode and decode, body hashing,
// validation and decoding one function result. None is a budget.
// How: each benchmark builds a generated input (manyTypes and the like) and
// times the operation over it.

package iface

import (
	"fmt"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// manyTypes interns n distinct types: arrays, refs and records over them.
func manyTypes(s *session, n int) []types.TypeID {
	out := make([]types.TypeID, 0, n)
	prev := types.I32
	for i := 0; len(out) < n; i++ {
		var t types.TypeID
		switch i % 4 {
		case 0:
			t = s.tab.Array(types.ConstArg{Kind: types.CAValue, Value: s.uintConst(uint64(i))}, prev)
		case 1:
			t = s.tab.Ref(prev)
		case 2:
			t = s.tab.Record([]types.RecordField{{Name: s.in.Intern(fmt.Sprintf("f%d", i)), Type: prev}, {Name: s.in.Intern("z"), Type: types.U8}})
		case 3:
			t = s.tab.Optional(prev)
		}
		out = append(out, t)
		if i%16 == 15 {
			prev = types.I32
		} else {
			prev = t
		}
	}
	return out
}

func BenchmarkTypeHash(b *testing.B) {
	s := newGeo(false)
	ts := manyTypes(s, 10_000)
	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h := NewHasher(s.tab, s.consts, nil)
			for _, t := range ts {
				h.Type(t)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(ts)), "ns/type")
	})
	b.Run("memoized", func(b *testing.B) {
		h := NewHasher(s.tab, s.consts, nil)
		for _, t := range ts {
			h.Type(t)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, t := range ts {
				h.Type(t)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(ts)), "ns/type")
	})
}

// bigModule is a generated module "big" of n struct and function items.
func bigModule(n int) (*session, *Interface) {
	s := newSession()
	f := s.sm.Add("big/a.mk", []byte("x"))
	s.anchors.Add(FileAnchorKey("big/a.mk"), Anchor{File: f.ID, Path: "big/a.mk", Starts: []uint32{0}, Ends: []uint32{1}})
	m := s.items.AddModule(&types.Module{Path: "big", Files: []source.FileID{f.ID}})
	var ids []types.ItemID
	for i := 0; i < n/2; i++ {
		st := s.item(m, types.ItemKey("big", fmt.Sprintf("S%d", i)), fmt.Sprintf("S%d", i), types.KindStruct, i%2 == 0)
		s.items.Get(st).Decl = &types.StructDecl{Fields: []types.FieldDecl{
			{Name: s.in.Intern("a"), Type: types.I64, Exported: true}, {Name: s.in.Intern("b"), Type: s.tab.Slice(types.U8)}},
			Shape: &types.ShapeTerm{Kind: types.TermLeaf, Leaf: types.Shape{Size: 24, Align: 8}}}
		fn := s.item(m, types.ItemKey("big", fmt.Sprintf("F%d", i)), fmt.Sprintf("F%d", i), types.KindFunc, i%2 == 1)
		st0 := s.tab.Named(st, nil)
		s.items.Get(fn).Decl = &types.FuncDecl{Sig: types.Signature{Item: fn, Name: s.in.Intern(fmt.Sprintf("F%d", i)),
			Params: []types.Param{{Name: s.in.Intern("x"), Type: s.tab.Ref(st0)}, {Name: s.in.Intern("n"), Type: types.Int}},
			Result: s.tab.Ref(types.I64), Flows: types.Flows{Sources: []types.Source{{Param: 1, Behind: true}}}, Exported: i%2 == 1}}
		ids = append(ids, st, fn)
	}
	return s, &Interface{Path: "big", Module: s.items.Module(m), Items: ids}
}

func BenchmarkEncodeInterface(b *testing.B) {
	s, m := bigModule(1000)
	b.ResetTimer()
	var n int
	for i := 0; i < b.N; i++ {
		n = len(EncodeInterface(s.env, s.h, m))
	}
	b.ReportMetric(float64(n), "bytes")
}

func BenchmarkDecodeInterface(b *testing.B) {
	s, m := bigModule(1000)
	enc := EncodeInterface(s.env, s.h, m)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		t := newSession()
		f := t.sm.Add("big/a.mk", []byte("x"))
		t.anchors.Add(FileAnchorKey("big/a.mk"), Anchor{File: f.ID, Path: "big/a.mk", Starts: []uint32{0}, Ends: []uint32{1}})
		b.StartTimer()
		if _, err := DecodeInterface(t.env, enc); err != nil {
			b.Fatal(err)
		}
	}
}

// bigBody is a straight-line body of n binary operations.
func bigBody(n int) *mir.Body {
	body := mir.NewBody(1, source.Span{}, types.I64)
	x := body.AddLocal(mir.Local{Type: types.I64, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	bb := body.AddBlock(mir.OriginBody, mir.NoDefer)
	st := make([]mir.Statement, n)
	for i := range st {
		st[i] = mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(x), Rvalue: mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add,
			A: mir.Copy(mir.LocalPlace(x)), B: mir.Imm(types.I64, uint64(i))}}
	}
	body.Blocks[bb].Stmts = st
	body.Blocks[bb].Term = mir.Terminator{Kind: mir.TermReturn}
	return body
}

func BenchmarkBodyHash(b *testing.B) {
	s := newSession()
	body := bigBody(64_000)
	h := NewHasher(s.tab, s.consts, nil)
	h.Body(body)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Body(body)
	}
}

func BenchmarkValidate(b *testing.B) {
	f := newWorldFixture()
	var reads []types.Read
	for i := 0; i < 1000; i++ {
		switch i % 4 {
		case 0:
			reads = append(reads, f.record(b, types.ReadSignature, "geo.Add", ""))
		case 1:
			reads = append(reads, f.record(b, types.ReadNoItem, "geo", fmt.Sprintf("Missing%d", i)))
		case 2:
			reads = append(reads, f.record(b, types.ReadFields, "geo.Point", ""))
		case 3:
			reads = append(reads, f.record(b, types.ReadTarget, "", "os"))
		}
	}
	w := f.world("app", types.X86_64Windows)
	if ok, r := Valid(w, reads); !ok {
		b.Fatalf("fixture reads do not hold: %v", r)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Valid(w, reads)
	}
}

// BenchmarkDecodeFuncResult decodes one function result of a 200-operation
// body into the session that encoded it, as a module hit's lazy body does:
// the per-result cost of a warm build.
func BenchmarkDecodeFuncResult(b *testing.B) {
	s := newSession()
	m := s.items.AddModule(&types.Module{Path: "big"})
	fn := s.item(m, types.ItemKey("big", "F"), "F", types.KindFunc, true)
	body := bigBody(200)
	body.Item = fn
	enc := EncodeFuncResult(s.env, s.h, &FuncResult{Item: types.ItemKey("big", "F"), Accept: true, Body: body})
	b.SetBytes(int64(len(enc)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeFuncResult(s.env, enc); err != nil {
			b.Fatal(err)
		}
	}
}
