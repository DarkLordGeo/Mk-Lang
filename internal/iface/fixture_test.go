// Why: encodings and hashes must not depend on IDs, so the tests need tables
// that can be filled in two different intern orders.
// What: session, a hand-built set of compiler tables, newGeo (the fixture
// module), fixture types, encode and decode helpers, tokens, golden and
// catchICE.
// How: newGeo with noise interns unrelated types, consts and items first and
// adds the items in reverse, so every ID differs from a plain build.

package iface

import (
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

var update = flag.Bool("update", false, "rewrite golden files")

// session is one compiler session's tables.
type session struct {
	in      *source.Interner
	items   *types.Items
	consts  *types.ConstTable
	tab     *types.Table
	sm      *source.SourceMap
	anchors *AnchorSet
	bodies  map[types.ItemID]*mir.Body
	h       *Hasher
	env     *Env
	mod     types.ModuleID
}

func newSession() *session {
	s := &session{in: source.NewInterner(), items: types.NewItems(), consts: types.NewConstTable(),
		sm: source.NewSourceMap(), anchors: NewAnchorSet(), bodies: make(map[types.ItemID]*mir.Body)}
	s.tab = types.NewTable(s.in, s.items, s.consts)
	s.h = NewHasher(s.tab, s.consts, func(id types.ItemID) *mir.Body { return s.bodies[id] })
	s.env = &Env{Interner: s.in, Types: s.tab, Items: s.items, Consts: s.consts, Sources: s.sm, PointerBytes: 8, Anchors: s.anchors}
	return s
}

// item adds an item of module m.
func (s *session) item(m types.ModuleID, key types.Key, name string, kind types.ItemKind, exported bool) types.ItemID {
	return s.items.AddItem(&types.Item{Module: m, Key: key, Name: s.in.Intern(name), Kind: kind, Exported: exported})
}

func (s *session) id(k string) types.ItemID {
	id, ok := s.items.ByKey(types.KeyFromString(k))
	if !ok {
		panic("no item " + k)
	}
	return id
}

func (s *session) uintConst(v uint64) types.ConstID {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return s.consts.Intern(types.ConstValue{Type: types.Uint, Align: 8, Bytes: b})
}

// geoItems are the items of the fixture module "geo", in declaration order.
var geoItems = []struct {
	key, name string
	kind      types.ItemKind
	exported  bool
}{
	{"geo.Point", "Point", types.KindStruct, true},
	{"geo.Wrap", "Wrap", types.KindStruct, true},
	{"geo.Shape", "Shape", types.KindEnum, true},
	{"geo.ErrA", "ErrA", types.KindError, true},
	{"geo.ErrB", "ErrB", types.KindError, false},
	{"geo.Add", "Add", types.KindFunc, true},
	{"geo.Main", "Main", types.KindFunc, false},
	{"geo.Main#fn0", "", types.KindLiteral, false},
	{"geo.Div", "Div", types.KindFunc, true},
	{"geo.Div#requires0", "", types.KindFunc, false},
}

// newGeo builds the fixture session. noise interns unrelated types, consts
// and items first and adds the items in reverse, so every ID differs from a
// session built with noise false.
func newGeo(noise bool) *session {
	s := newSession()
	if noise {
		z := s.items.AddModule(&types.Module{Path: "zzz"})
		for i := 0; i < 7; i++ {
			s.tab.Array(types.ConstArg{Kind: types.CAValue, Value: s.uintConst(uint64(100 + i))}, types.U16)
		}
		s.item(z, types.KeyFromString("zzz.Noise"), "Noise", types.KindStruct, true)
		s.tab.Record([]types.RecordField{{Name: s.in.Intern("zz"), Type: types.Bool}})
	}
	s.mod = s.items.AddModule(&types.Module{Path: "geo"})
	order := make([]int, len(geoItems))
	for i := range order {
		order[i] = i
		if noise {
			order[i] = len(geoItems) - 1 - i
		}
	}
	for _, i := range order {
		g := geoItems[i]
		s.item(s.mod, types.KeyFromString(g.key), g.name, g.kind, g.exported)
	}
	return s
}

// fixtureTypes is a list of types covering every mk.ty.v1 tag, with a label.
func fixtureTypes(s *session) []types.TypeID {
	t := s.tab
	point := t.Named(s.id("geo.Point"), nil)
	errs := t.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrB")}, {Item: s.id("geo.ErrA")}})
	errA := t.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrA")}})
	paramErrs := t.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrB")}, {IsParam: true, Param: 0}})
	four := types.ConstArg{Kind: types.CAValue, Value: s.uintConst(4)}
	fnT := t.Fn(types.FnType{
		Params:  []types.FnParam{{Type: types.I32}, {Type: t.Ref(point), Into: 1}},
		Result:  types.Bool,
		Errors:  errA,
		Sources: []types.Source{{Param: 2, Behind: true, Path: []types.FieldStep{{Field: 1, Type: types.F64}}}},
		Stores:  []types.Store{{From: 2, Into: 1}},
		Ends:    []types.ParamIndex{2},
	})
	return []types.TypeID{
		types.I8, types.I16, types.I32, types.I64, types.U8, types.U16, types.U32, types.U64,
		types.Int, types.Uint, types.F32, types.F64, types.Bool, types.Char, types.Void, types.Never, types.Str,
		t.Ref(types.I32), t.Mut(types.I32), t.Slice(types.U8), t.MutSlice(types.U8), t.RawPtr(types.U8),
		t.Optional(types.I32), t.Array(four, types.U8),
		t.Array(types.ConstArg{Kind: types.CAParam, Param: 1}, types.U8),
		t.Array(types.ConstArg{Kind: types.CASizeOf, Type: t.Param(0)}, types.U8),
		t.ErrorUnion(errA, types.I32), t.ErrorUnion(errs, types.Void), t.ErrorSetType(errs), t.ErrorSetType(paramErrs),
		t.Record([]types.RecordField{{Name: s.in.Intern("y"), Type: types.F64}, {Name: s.in.Intern("x"), Type: types.I32}}),
		fnT, t.Fn(types.FnType{Result: types.Void}),
		point, t.Named(s.id("geo.Wrap"), []types.GenericArg{types.TypeArg(types.I32)}),
		t.Named(s.id("geo.Wrap"), []types.GenericArg{types.ConstGenericArg(four)}),
		t.Named(s.id("geo.Shape"), nil),
		t.Param(0), t.SelfType(), t.Param(1), t.Optional(t.Ref(point)), t.Optional(fnT),
	}
}

// encodeBundle is a store-form record of types, consts and spans (tests only).
func encodeBundle(s *session, tys []types.TypeID, cs []types.ConstID, spans []source.Span, prefer ...AnchorKey) []byte {
	p := newPools(s.h, s.env, types.NoModule)
	p.prefer = prefer
	var e Encoder
	w := walker{e: &e, h: s.h, p: p}
	e.Uvarint(uint64(len(tys)))
	for _, t := range tys {
		w.typ(t)
	}
	e.Uvarint(uint64(len(cs)))
	for _, c := range cs {
		w.cnst(c)
	}
	e.Uvarint(uint64(len(spans)))
	for _, sp := range spans {
		w.span(sp)
	}
	return p.finish(recValues, e.Out())
}

func decodeBundle(env *Env, b []byte) (tys []types.TypeID, cs []types.ConstID, spans []source.Span, err error) {
	err = decodeRecord(env, b, recValues, nil, func(r *reader) error {
		tys, cs, spans = nil, nil, nil
		d := r.d
		n := d.Count(1)
		for i := 0; i < n; i++ {
			tys = append(tys, r.typ(d, 0))
		}
		n = d.Count(1)
		for i := 0; i < n; i++ {
			cs = append(cs, r.cnst(d, 0))
		}
		n = d.Count(1)
		for i := 0; i < n; i++ {
			spans = append(spans, r.span(d))
		}
		return d.Err
	})
	return
}

// tokens is a toy tokenizer for anchor tests: identifiers and numbers, and
// single punctuation bytes; whitespace and // comments are trivia.
func tokens(text string) (starts, ends []uint32, texts []string) {
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == ' ' || c == '\n' || c == '\t':
			i++
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(text) && (text[j] == '_' || text[j] >= '0' && text[j] <= '9' || text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z') {
				j++
			}
			starts, ends, texts = append(starts, uint32(i)), append(ends, uint32(j)), append(texts, text[i:j])
			i = j
		default:
			starts, ends, texts = append(starts, uint32(i)), append(ends, uint32(i+1)), append(texts, text[i:i+1])
			i++
		}
	}
	return
}

// addFile adds a file and one item anchor over all its tokens.
func (s *session) addFile(path, text string, key AnchorKey) *source.File {
	f := s.sm.Add(path, []byte(text))
	st, en, tx := tokens(text)
	s.anchors.Add(key, Anchor{File: f.ID, Path: path, Starts: st, Ends: en,
		TokenHash: source.HashString(DomTokens, strings.Join(tx, "\x00"))})
	return f
}

// golden compares got with testdata/name, or rewrites it under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from the golden file; rerun with -update and read the diff", name)
	}
}

// catchICE runs f and reports whether it raised an ICE.
func catchICE(f func()) bool {
	return source.Guard("test", source.Span{}, f) != nil
}
