// Why: the cache's records decide whether a build may reuse earlier work, so
// each must round-trip and none may depend on intern order.
// What: tests of the interface record, function results, evaluation records,
// generator outputs, parse summaries, manifests, keys, the build ID and the
// error-code collision check.
// How: encodes in one session and decodes into a fresh one (loadSession), and
// hashes under two intern orders (ifaceSession with and without noise) and
// concurrent goroutine schedules.

package iface

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// addBase adds module "base" with exported struct base.Unit.
func addBase(s *session) types.ItemID {
	m := s.items.AddModule(&types.Module{Path: "base"})
	u := s.item(m, types.KeyFromString("base.Unit"), "Unit", types.KindStruct, true)
	s.items.Get(u).Decl = &types.StructDecl{Fields: []types.FieldDecl{{Name: s.in.Intern("v"), Type: types.U8, Exported: true}}}
	return u
}

// ifaceSession is the geo session over base, with geo's file and anchors.
func ifaceSession(noise bool) (*session, *source.File) {
	s := newGeo(noise)
	if noise {
		s.sm.Add("noise.mk", []byte("x"))
	}
	u := addBase(s)
	withDecls(s)
	f := s.addFile("geo/main.mk", bodyText, "geo.Main")
	s.anchors.Add(FileAnchorKey("geo/main.mk"), Anchor{File: f.ID, Path: "geo/main.mk"})
	mod := s.items.Module(s.mod)
	mod.Files = []source.FileID{f.ID}
	mod.UnsafePermitted = true
	pt := s.items.Get(s.id("geo.Point"))
	pt.Decl.(*types.StructDecl).Fields = append(pt.Decl.(*types.StructDecl).Fields,
		types.FieldDecl{Name: s.in.Intern("unit"), Type: s.tab.Named(u, nil), Exported: true})
	i := strings.Index(bodyText, "Main")
	pt.Span = source.NewSpan(f.ID, uint32(i), uint32(i+4))
	s.items.Get(s.id("geo.Main#fn0")).Owner = s.id("geo.Main")
	k := s.item(s.mod, types.KeyFromString("geo.K"), "K", types.KindConst, true)
	s.items.Get(k).Decl = &types.ConstDecl{Type: types.Uint, Fuel: types.DefaultFuel, Value: s.uintConst(7), HasValue: true}
	return s, f
}

func geoInterface(s *session, f *source.File) *Interface {
	var ids []types.ItemID
	for i := 1; i < s.items.NumItems(); i++ {
		if it := s.items.Get(types.ItemID(i)); it.Module == s.mod {
			ids = append(ids, it.ID)
		}
	}
	i := strings.Index(bodyText, "a[i]")
	d := diag.New(diag.E0301, source.NewSpan(f.ID, uint32(i), uint32(i+4)), "mismatched types").Text("expected", "int")
	return &Interface{
		Path: "geo", Module: s.items.Module(s.mod), Items: ids,
		Shapes: []ShapeRec{{Item: s.id("geo.Point"), Shape: types.Shape{Size: 24, Align: 8, Send: true, Share: true, Freeze: true}},
			{Item: s.id("geo.Shape"), Shape: types.Shape{Size: 16, Align: 8, Send: true, Share: true, Freeze: true}}},
		Bodies: []BodyRef{{Item: types.KeyFromString("geo.Div#requires0"), BodyHash: s.h.Body(s.bodies[s.id("geo.Div#requires0")]),
			Entry: source.HashString("t", "req")}},
		Funcs:         []FuncRef{{Item: types.KeyFromString("geo.Main"), Entry: source.HashString("t", "main"), HasDiags: true}, {Item: types.KeyFromString("geo.Add"), Entry: source.HashString("t", "add")}},
		Gens:          []GenRef{{Gen: types.GenKey("geo", 0), Entry: source.HashString("t", "gen")}},
		GenText:       source.HashString("t", "gentext"),
		Diags:         []diag.Diagnostic{*d},
		ExternalReads: []types.Read{{Kind: types.ReadSignature, Key: types.KeyFromString("base.Unit"), Hash: s.h.Decl(s.items.Get(s.id("base.Unit")))}},
	}
}

// loadSession is a fresh session holding only geo's imports (base) and its
// file and anchors, as the driver would before a module lookup.
func loadSession(text string) *session {
	s := newSession()
	s.sm.Add("first.mk", []byte("y"))
	addBase(s)
	f := s.addFile("geo/main.mk", text, "geo.Main")
	s.anchors.Add(FileAnchorKey("geo/main.mk"), Anchor{File: f.ID, Path: "geo/main.mk"})
	return s
}

// loadContracts decodes the contract bodies the way a body store hit does.
func loadContracts(t *testing.T, from, into *session) {
	t.Helper()
	req := from.id("geo.Div#requires0")
	enc := EncodeFuncResult(from.env, from.h, &FuncResult{Item: types.KeyFromString("geo.Div#requires0"), Accept: true, Body: from.bodies[req]})
	fr, err := DecodeFuncResult(into.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	into.bodies[into.id("geo.Div#requires0")] = fr.Body
	into.bodies[into.id("geo.Main#fn0")] = retBody(into, into.id("geo.Main#fn0"), types.Bool)
}

func TestRoundTripInterface(t *testing.T) {
	a, f := ifaceSession(false)
	m := geoInterface(a, f)
	enc := EncodeInterface(a.env, a.h, m)
	b := loadSession(bodyText)
	got, err := DecodeInterface(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	loadContracts(t, a, b)
	if got.Path != "geo" || !got.Module.UnsafePermitted || got.Module.Std || len(got.Module.Files) != 1 {
		t.Errorf("module header: %+v", got.Module)
	}
	if len(got.Items) != len(m.Items) {
		t.Fatalf("%d items, want %d", len(got.Items), len(m.Items))
	}
	for _, id := range m.Items {
		ia := a.items.Get(id)
		ib := b.items.Get(b.id(ia.Key.String()))
		if ia.Kind != ib.Kind || ia.Exported != ib.Exported || a.in.Str(ia.Name) != b.in.Str(ib.Name) {
			t.Errorf("%s: header differs", ia.Key)
		}
		if (ia.Owner == types.NoItem) != (ib.Owner == types.NoItem) {
			t.Errorf("%s: owner differs", ia.Key)
		}
		if ia.Decl != nil && a.h.Decl(ia) != b.h.Decl(ib) {
			t.Errorf("%s: decl hash differs", ia.Key)
		}
	}
	if pt := b.items.Get(b.id("geo.Point")); b.sm.Text(pt.Span) != "Main" {
		t.Errorf("item span decoded onto %q", b.sm.Text(pt.Span))
	}
	if got.APIHash != m.APIHash || got.ShapeHash != m.ShapeHash || m.APIHash.IsZero() {
		t.Error("stored part hashes differ")
	}
	if b.h.APIHash(got.Items) != m.APIHash || b.h.ShapesHash(got.Items, got.Shapes) != m.ShapeHash {
		t.Error("part hashes recomputed in the loading session differ")
	}
	if !reflect.DeepEqual(got.Funcs, []FuncRef{m.Funcs[1], m.Funcs[0]}) || !reflect.DeepEqual(got.Bodies, m.Bodies) ||
		!reflect.DeepEqual(got.Gens, m.Gens) || got.GenText != m.GenText || !reflect.DeepEqual(got.ExternalReads, m.ExternalReads) {
		t.Error("refs or reads differ")
	}
	if len(got.Diags) != 1 || b.sm.Text(got.Diags[0].At) != "a[i]" {
		t.Errorf("diagnostics: %+v", got.Diags)
	}
	if len(got.Anchors) == 0 {
		t.Error("no anchors listed")
	}
	if re := EncodeInterface(b.env, b.h, got); !bytes.Equal(re, enc) {
		t.Error("re-encoding the decoded interface changed the bytes")
	}
	if _, err := DecodeInterface(b.env, enc); !errors.Is(err, errLoaded) {
		t.Errorf("decoding a loaded module again: %v", err)
	}
}

func TestSigHashIndependentOfBodyAvailability(t *testing.T) {
	a, f := ifaceSession(false)
	enc := EncodeInterface(a.env, a.h, geoInterface(a, f))
	cold := a.h.Signature(&a.items.Get(a.id("geo.Div")).Decl.(*types.FuncDecl).Sig)
	b := loadSession(bodyText)
	if _, err := DecodeInterface(b.env, enc); err != nil {
		t.Fatal(err)
	}
	sig := &b.items.Get(b.id("geo.Div")).Decl.(*types.FuncDecl).Sig
	if !catchICE(func() { b.h.Signature(sig) }) {
		t.Error("a signature hash with its contract body absent did not raise an ICE")
	}
	loadContracts(t, a, b)
	if b.h.Signature(sig) != cold {
		t.Error("the warm signature hash differs from the cold one (INC-05)")
	}
}

func TestAPIExcludesPrivate(t *testing.T) {
	a, f := ifaceSession(false)
	base := geoInterface(a, f)
	EncodeInterface(a.env, a.h, base)
	api, shape := base.APIHash, base.ShapeHash
	again := func() *Interface { m := geoInterface(a, f); EncodeInterface(a.env, a.h, m); return m }
	// A private item's signature, a body, a diagnostic and a span.
	a.items.Get(a.id("geo.Main")).Decl.(*types.FuncDecl).Sig.Result = types.I32
	a.bodies[a.id("geo.Main#fn0")].Blocks[0].Term.Exit = mir.ExitFail
	if m := again(); m.APIHash != api || m.ShapeHash != shape {
		t.Error("a private item or body change changed the API or shape hash")
	}
	// A private field rename and a private field type change of an
	// exported struct, with the private bit already set.
	wrap := a.items.Get(a.id("geo.Wrap")).Decl.(*types.StructDecl)
	wrap.Fields[1].Name = a.in.Intern("hidden")
	if m := again(); m.APIHash != api || m.ShapeHash != shape {
		t.Error("a private field rename changed the API or shape hash")
	}
	// An exported signature change.
	a.items.Get(a.id("geo.Add")).Decl.(*types.FuncDecl).Sig.Result = types.I64
	if m := again(); m.APIHash == api {
		t.Error("an exported signature change left the API hash equal")
	}
	// An exported const value change.
	b, fb := ifaceSession(false)
	b.items.Get(b.id("geo.K")).Decl.(*types.ConstDecl).Value = b.uintConst(8)
	if m := geoInterface(b, fb); EncodeInterface(b.env, b.h, m) != nil && m.APIHash == api {
		t.Error("an exported const value change left the API hash equal")
	}
	// A shape record change.
	c, fc := ifaceSession(false)
	mc := geoInterface(c, fc)
	mc.Shapes[0].Shape.Size = 32
	EncodeInterface(c.env, c.h, mc)
	if mc.ShapeHash == shape || mc.APIHash != api {
		t.Error("a shape change did not change exactly the shape hash")
	}
}

func TestDecodeMissingForeignIsStale(t *testing.T) {
	a, f := ifaceSession(false)
	enc := EncodeInterface(a.env, a.h, geoInterface(a, f))
	b := newSession()
	fb := b.addFile("geo/main.mk", bodyText, "geo.Main")
	b.anchors.Add(FileAnchorKey("geo/main.mk"), Anchor{File: fb.ID, Path: "geo/main.mk"})
	before := b.items.NumItems()
	if _, err := DecodeInterface(b.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if b.items.NumItems() != before || b.items.NumModules() != 1 {
		t.Error("a stale decode left items or a module behind")
	}
	// Edited tokens in geo's own file: stale too.
	c := loadSession(strings.Replace(bodyText, "a[i]", "(a[i])", 1))
	if _, err := DecodeInterface(c.env, enc); !errors.Is(err, ErrStale) {
		t.Fatalf("edited tokens: err = %v, want ErrStale", err)
	}
	// Truncations are corrupt and leave nothing behind.
	for _, cut := range []int{len(enc) / 3, len(enc) / 2, len(enc) - 1} {
		d := loadSession(bodyText)
		n := d.items.NumItems()
		if _, err := DecodeInterface(d.env, enc[:cut]); !errors.Is(err, ErrCorrupt) {
			t.Errorf("cut at %d: %v", cut, err)
		}
		if d.items.NumItems() != n {
			t.Errorf("cut at %d: items left behind", cut)
		}
	}
}

func TestRoundTripFuncResult(t *testing.T) {
	a, f := ifaceSession(false)
	body := everyBody(a, f)
	i := strings.Index(bodyText, "g(f)")
	d := diag.New(diag.E0501, source.NewSpan(f.ID, uint32(i), uint32(i+4)), "conflict")
	var reads types.ReadSet
	reads.Add(types.Read{Kind: types.ReadBody, Key: types.KeyFromString("geo.Add"), Hash: source.HashString("t", "b")})
	reads.Add(types.Read{Kind: types.ReadSignature, Key: types.KeyFromString("geo.Main")})
	r := &FuncResult{Item: types.KeyFromString("geo.Main"), Diags: []diag.Diagnostic{*d}, Body: body, Borrow: []byte{1, 2, 3}, Reads: reads}
	enc := EncodeFuncResult(a.env, a.h, r)
	got, err := DecodeFuncResult(a.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Body, body) || !reflect.DeepEqual(got.Diags, r.Diags) || !bytes.Equal(got.Borrow, r.Borrow) ||
		got.Reads.Hash() != reads.Hash() || got.Item != r.Item || got.Accept {
		t.Error("function result differs after a same-session round trip")
	}
	if len(got.Anchors) != 1 || got.Anchors[0].Key != "geo.Main" {
		t.Errorf("anchors %v", got.Anchors)
	}
	b, _ := ifaceSession(true)
	got, err = DecodeFuncResult(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if re := EncodeFuncResult(b.env, b.h, got); !bytes.Equal(re, enc) {
		t.Error("re-encoding the function result in a fresh session changed the bytes")
	}
	// A rejected-before-MIR result has no body.
	enc = EncodeFuncResult(a.env, a.h, &FuncResult{Item: types.KeyFromString("geo.Add"), Accept: false})
	if got, err := DecodeFuncResult(a.env, enc); err != nil || got.Body != nil || got.Borrow != nil {
		t.Errorf("empty result: %v %+v", err, got)
	}
}

func TestEvalRecordAnchors(t *testing.T) {
	a, f := ifaceSession(false)
	foreign := "fn Helper(a: int): int {\n  return a / 0\n}\n"
	g := a.addFile("g/helper.mk", foreign, "g.Helper")
	at := strings.Index(foreign, "a / 0")
	site := strings.Index(bodyText, "a[i]")
	x := &types.EvalError{Kind: types.EvalPanic, Panic: types.PanicDivideByZero, Message: "divide by zero", Steps: 12,
		At:    source.NewSpan(g.ID, uint32(at), uint32(at+5)),
		Trace: []source.Span{source.NewSpan(f.ID, uint32(site), uint32(site+4)), source.NewSpan(g.ID, uint32(at), uint32(at+5))},
		Items: []types.ItemID{a.id("geo.Main"), types.NoItem}}
	rec := &EvalRecord{Err: x, Steps: 12, Site: "geo.Main"}
	rec.Reads.Add(types.Read{Kind: types.ReadBody, Key: types.KeyFromString("g.Helper"), Hash: source.HashString("t", "h")})
	enc := EncodeEvalRecord(a.env, a.h, rec)
	got, err := DecodeEvalRecord(a.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Err, x) || got.Steps != 12 || got.Reads.Hash() != rec.Reads.Hash() || got.Value != types.NoConst {
		t.Errorf("eval record differs: %+v", got.Err)
	}
	keys := []AnchorKey{}
	for _, u := range got.Anchors {
		keys = append(keys, u.Key)
	}
	if !reflect.DeepEqual(keys, []AnchorKey{"g.Helper", "geo.Main"}) {
		t.Errorf("anchors %v, want the foreign item and the requesting one", keys)
	}
	// return a / 0 becomes return (a) / 0: the body hash may not see it,
	// the anchor does.
	b, _ := ifaceSession(false)
	b.addFile("g/helper.mk", strings.Replace(foreign, "a / 0", "(a) / 0", 1), "g.Helper")
	if _, err := DecodeEvalRecord(b.env, enc); !errors.Is(err, ErrStale) {
		t.Errorf("eval record over changed tokens: %v, want ErrStale", err)
	}
	// A value record.
	v := EncodeEvalRecord(a.env, a.h, &EvalRecord{Value: strConst(a, "ok"), Steps: 3})
	if got, err := DecodeEvalRecord(a.env, v); err != nil || a.tab.ConstString(got.Value) != a.tab.ConstString(strConst(a, "ok")) {
		t.Errorf("value record: %v", err)
	}
	// The same for a generator output whose diagnostic cites the generator.
	gd := diag.New(diag.E0903, source.NewSpan(g.ID, uint32(at), uint32(at+5)), "generator failed")
	out := &GenOutput{Gen: types.GenKey("geo", 0), Items: []GenItem{{Name: "UserEq", Text: "impl Eq for User {}", Hash: source.HashString(DomTokens, "x")}},
		Diags: []diag.Diagnostic{*gd}, Steps: 9}
	ge := EncodeGenOutput(a.env, a.h, out)
	if got, err := DecodeGenOutput(a.env, ge); err != nil || !reflect.DeepEqual(got.Items, out.Items) || got.Steps != 9 || len(got.Diags) != 1 {
		t.Errorf("generator output: %v %+v", err, got)
	}
	if _, err := DecodeGenOutput(b.env, ge); !errors.Is(err, ErrStale) {
		t.Errorf("generator output over changed tokens: %v, want ErrStale", err)
	}
}

func TestParseSummaryNotShared(t *testing.T) {
	build := source.HashString("t", "build")
	content := source.HashString("t", "content")
	keys := map[source.Hash]bool{}
	for _, m := range []string{"a", "b"} {
		for _, p := range []string{"util.mk", "x/util.mk"} {
			keys[ParseKey(build, m, p, content)] = true
		}
	}
	if len(keys) != 4 {
		t.Errorf("%d distinct parse keys, want 4", len(keys))
	}
	st, en, _ := tokens(spanText1)
	p := &ParseSummary{ContentHash: content, ImportsHash: ImportsHash([][2]string{{"fs", "std.fs"}}),
		Items:      []ParsedItem{{Name: "F", Key: "a.F", Anchor: Anchor{File: 7, Path: "a/util.mk", Starts: st, Ends: en, TokenHash: build}}},
		FileAnchor: Anchor{File: 7, Path: "a/util.mk"},
		Imports:    []ParsedImport{{Binding: "fs", Path: "std.fs", Node: 3, Start: 0, End: 13, PathStart: 7, PathEnd: 13}},
		Main:       true, Exports: []string{"F", "T"}, Unsafe: []ParsedUnsafe{{Start: 20, End: 26}, {Start: 40, End: 46, Impl: true}}}
	enc := EncodeParseSummary(p)
	got, err := DecodeParseSummary(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].Anchor.File != source.NoFile || got.FileAnchor.File != source.NoFile {
		t.Error("a FileID was stored")
	}
	sm := source.NewSourceMap()
	sm.Add("other.mk", nil)
	f := sm.Add("a/util.mk", []byte(spanText1))
	if !got.Bind(sm) || got.Items[0].Anchor.File != f.ID || got.FileAnchor.File != f.ID {
		t.Error("Bind did not set the session's FileID")
	}
	p.Items[0].Anchor.File, p.FileAnchor.File = f.ID, f.ID
	if !reflect.DeepEqual(got, p) {
		t.Errorf("summary differs:\n got %+v\nwant %+v", got, p)
	}
	if _, err := DecodeParseSummary(enc[:len(enc)-1]); err == nil {
		t.Error("a truncated summary decoded")
	}
}

func TestManifestAddDeterministic(t *testing.T) {
	cand := func(i int) Candidate {
		return Candidate{Reads: []types.Read{{Kind: types.ReadTarget, Name: "os", Hash: source.HashString("t", fmt.Sprint(i))}},
			Entry: source.HashString("e", fmt.Sprint(i))}
	}
	var m1, m2 Manifest
	for i := 0; i < 12; i++ {
		m1.Add(cand(i))
		m2.Add(cand(i))
	}
	m1.Add(cand(9)) // re-adding moves it to the front, once
	m2.Add(cand(9))
	if !bytes.Equal(EncodeManifest(&m1), EncodeManifest(&m2)) {
		t.Error("equal Add sequences encode differently")
	}
	if len(m1.Candidates) != MaxCandidates || m1.Candidates[0].Entry != cand(9).Entry {
		t.Errorf("%d candidates, first %v", len(m1.Candidates), m1.Candidates[0].Entry)
	}
	seen := map[source.Hash]bool{}
	for _, c := range m1.Candidates {
		if seen[c.Entry] {
			t.Error("a duplicate candidate")
		}
		seen[c.Entry] = true
	}
}

func TestManifestUpdatePersists(t *testing.T) {
	a := Candidate{Reads: []types.Read{{Kind: types.ReadPkg, Name: "x"}}, Entry: source.HashString("e", "A")}
	b := Candidate{Reads: []types.Read{{Kind: types.ReadPkg, Name: "y"}}, Entry: source.HashString("e", "B")}
	m := &Manifest{}
	m.Add(a)
	dec, err := DecodeManifest(EncodeManifest(m))
	if err != nil {
		t.Fatal(err)
	}
	dec.Add(b)
	got, err := DecodeManifest(EncodeManifest(dec))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 2 || got.Candidates[0].Entry != b.Entry || got.Candidates[1].Entry != a.Entry {
		t.Errorf("manifest after Add: %+v, want [B, A]", got.Candidates)
	}
}

func TestKeysSortInputs(t *testing.T) {
	build := source.HashString("t", "b")
	f1 := FileTokens{Path: "b.mk", Items: []ItemTokens{{Key: types.KeyFromString("m.Y")}, {Key: types.KeyFromString("m.X")}}}
	f2 := FileTokens{Path: "a.mk", Outside: build}
	i1, i2 := ImportHash{Path: "std.fs"}, ImportHash{Path: "c", API: build}
	k1 := ModulePrefix(build, "m", []FileTokens{f1, f2}, []ImportHash{i1, i2})
	f1r := f1
	f1r.Items = []ItemTokens{f1.Items[1], f1.Items[0]}
	k2 := ModulePrefix(build, "m", []FileTokens{f2, f1r}, []ImportHash{i2, i1})
	if k1 != k2 {
		t.Error("ModulePrefix depends on input order")
	}
	if ModulePrefix(build, "n", []FileTokens{f1, f2}, nil) == ModulePrefix(build, "m", []FileTokens{f1, f2}, nil) {
		t.Error("ModulePrefix ignores the module path")
	}
	r1 := []types.Read{{Kind: types.ReadPkg, Name: "b"}, {Kind: types.ReadPkg, Name: "a"}}
	r2 := []types.Read{r1[1], r1[0], r1[0]}
	p := FuncPrefix(build, types.KeyFromString("m.F"), build, build)
	if FuncKey(p, r1) != FuncKey(p, r2) || InterfaceKey(p, r1) != InterfaceKey(p, r2) {
		t.Error("full keys depend on read order")
	}
	if FuncKey(p, r1) == InterfaceKey(p, r1) || EvalKey(p, r1) == GenKey(p, r1) {
		t.Error("two entry families share a key")
	}
	if ImportsHash([][2]string{{"a", "x"}, {"b", "y"}}) != ImportsHash([][2]string{{"b", "y"}, {"a", "x"}}) {
		t.Error("ImportsHash depends on order")
	}
	e1 := EvalKeyInput{What: types.EvalConst, Item: types.KeyFromString("m.N"), Fuel: 10, Module: "m"}
	e2 := e1
	e2.Stage = types.StageBodies
	if EvalPrefix(build, e1) == EvalPrefix(build, e2) {
		t.Error("EvalPrefix ignores the stage")
	}
	g1 := GenKeyInput{Gen: types.GenKey("m", 0), Generator: types.KeyFromString("g.Eq"), Target: "x86_64-linux"}
	g2 := g1
	g2.Target = "x86_64-windows"
	if GenPrefix(build, g1) == GenPrefix(build, g2) {
		t.Error("GenPrefix ignores the target")
	}
	if LibKey("x86_64-windows", build) == LibKey("x86_64-linux", build) || LayoutKey("clang 21", "a") == LayoutKey("clang 18", "a") {
		t.Error("lib or layout keys ignore an input")
	}
}

func TestPosKeyPerTarget(t *testing.T) {
	b, tc, rows := source.HashString("t", "b"), source.HashString("t", "tc"), source.HashString("t", "rows")
	if PosKey(b, tc, "x86_64-windows", rows) == PosKey(b, tc, "x86_64-linux", rows) {
		t.Error("PosKey ignores the target")
	}
	if PosKey(b, tc, "x86_64-linux", rows) == PosKey(b, source.HashString("t", "tc2"), "x86_64-linux", rows) {
		t.Error("PosKey ignores the toolchain")
	}
	if PosKey(b, tc, "x86_64-linux", rows) != PosKey(b, tc, "x86_64-linux", source.HashString("t", "rows")) {
		t.Error("equal rows give different keys")
	}
}

func TestPackKeyHoldsRows(t *testing.T) {
	h := func(s string) source.Hash { return source.HashString("t", s) }
	in := PackKeyInput{Build: h("b"), Toolchain: h("tc"), Target: "x86_64-linux", Profile: types.Release, CPU: "x86-64-v2",
		Instances: []PackInstance{{Name: h("n1"), Content: h("c1"), Rows: h("r1")}, {Name: h("n0"), Content: h("c0"), Rows: h("r0")}},
		Inlined:   []InlinedBody{{Body: h("ib"), Rows: h("ir")}}}
	k := PackKey(in)
	rev := in
	rev.Instances = []PackInstance{in.Instances[1], in.Instances[0]}
	if PackKey(rev) != k {
		t.Error("PackKey depends on instance order")
	}
	rows := in
	rows.Instances = []PackInstance{in.Instances[0], {Name: h("n0"), Content: h("c0"), Rows: h("moved")}}
	if PackKey(rows) == k {
		t.Error("an instance's rows are not in the pack key (I-9)")
	}
	inl := in
	inl.Inlined = []InlinedBody{{Body: h("ib"), Rows: h("moved")}}
	if PackKey(inl) == k {
		t.Error("an inlined body's rows are not in the pack key (INC-08)")
	}
	for name, edit := range map[string]func(p *PackKeyInput){
		"profile": func(p *PackKeyInput) { p.Profile = types.Dev }, "cpu": func(p *PackKeyInput) { p.CPU = "znver4" },
		"pgo": func(p *PackKeyInput) { p.PGO = h("prof") }, "instrumented": func(p *PackKeyInput) { p.Instrumented = true },
	} {
		c := in
		edit(&c)
		if PackKey(c) == k {
			t.Errorf("PackKey ignores the %s", name)
		}
	}
	nat := in
	nat.CPU = "native"
	if !catchICE(func() { PackKey(nat) }) {
		t.Error("a native CPU reached a pack key (AR086)")
	}
}

func TestBuildIDFromPure(t *testing.T) {
	id := BuildIDFrom(source.HashString("t", "mk.exe"))
	if id != BuildIDFrom(source.HashString("t", "mk.exe")) || id == BuildIDFrom(source.HashString("t", "mk2.exe")) {
		t.Error("BuildIDFrom is not a function of the executable hash")
	}
	golden(t, "buildid.golden", []byte(id.String()+"\n"))
}

func TestArchErrorCodeCollision(t *testing.T) {
	// Find two keys whose 31-bit codes collide (a birthday search).
	seen := map[uint32]types.Key{}
	var x, y types.Key
	for i := 0; ; i++ {
		k := types.KeyFromString(fmt.Sprintf("m.E%d", i))
		c := types.ErrorCode(k)
		if o, ok := seen[c]; ok {
			x, y = o, k
			break
		}
		seen[c] = k
	}
	if x.Compare(y) > 0 {
		x, y = y, x
	}
	a, b, found := ErrorCodeCollision([]types.Key{types.KeyFromString("m.Z"), y, types.KeyFromString("m.A"), x, x})
	if !found || a != x || b != y {
		t.Errorf("collision (%v, %v, %v), want (%v, %v)", a, b, found, x, y)
	}
	if _, _, found := ErrorCodeCollision([]types.Key{types.KeyFromString("m.A"), types.KeyFromString("m.B"), types.KeyFromString("m.A")}); found {
		t.Error("distinct codes reported as a collision")
	}
}

func TestHashIgnoresInternOrder(t *testing.T) {
	a, fa := ifaceSession(false)
	b, fb := ifaceSession(true)
	ta, tb := fixtureTypes(a), fixtureTypes(b)
	for i := range ta {
		if a.h.Type(ta[i]) != b.h.Type(tb[i]) {
			t.Errorf("type %d hash depends on intern order", i)
		}
	}
	ca, cb := fixtureConsts(a), fixtureConsts(b)
	for i := range ca {
		if a.h.Const(ca[i]) != b.h.Const(cb[i]) {
			t.Errorf("const %d hash depends on intern order", i)
		}
	}
	for _, g := range geoItems {
		ia, ib := a.items.Get(a.id(g.key)), b.items.Get(b.id(g.key))
		if ia.Decl != nil && a.h.Decl(ia) != b.h.Decl(ib) {
			t.Errorf("%s decl hash depends on intern order", g.key)
		}
	}
	terms := fixtureTerms(a)
	termsB := fixtureTerms(b)
	for i := range terms {
		if a.h.ShapeTerm(&terms[i]) != b.h.ShapeTerm(&termsB[i]) {
			t.Errorf("term %d hash depends on intern order", i)
		}
	}
	la, lb := fixtureLayouts(a), fixtureLayouts(b)
	for i := range la {
		if a.h.Layout(&la[i]) != b.h.Layout(&lb[i]) {
			t.Errorf("layout %d hash depends on intern order", i)
		}
	}
	if a.h.Body(everyBody(a, fa)) != b.h.Body(everyBody(b, fb)) {
		t.Error("body hash depends on intern order")
	}
	ea := EncodeInterface(a.env, a.h, geoInterface(a, fa))
	eb := EncodeInterface(b.env, b.h, geoInterface(b, fb))
	if !bytes.Equal(ea, eb) {
		t.Error("interface bytes depend on intern order")
	}
	fra := EncodeFuncResult(a.env, a.h, &FuncResult{Item: types.KeyFromString("geo.Main"), Body: everyBody(a, fa)})
	frb := EncodeFuncResult(b.env, b.h, &FuncResult{Item: types.KeyFromString("geo.Main"), Body: everyBody(b, fb)})
	if !bytes.Equal(fra, frb) {
		t.Error("function result bytes depend on intern order")
	}
	// And in eight goroutine schedules over one cold Hasher.
	c, fc := ifaceSession(true)
	d, fd := ifaceSession(false)
	irSchedules(t, d, fd, c, fc)
}

// TestEncodeTypesRoundTrip: EncodeTypes and DecodeTypes carry every fixture
// type into a second session with equal hashes; a session that lacks a
// named item is ErrStale.
func TestEncodeTypesRoundTrip(t *testing.T) {
	a := newGeo(false)
	tys := fixtureTypes(a)
	enc := EncodeTypes(a.env, a.h, tys)
	b := newGeo(true)
	got, err := DecodeTypes(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(tys) {
		t.Fatalf("decoded %d types, want %d", len(got), len(tys))
	}
	for i := range tys {
		if a.h.Type(tys[i]) != b.h.Type(got[i]) {
			t.Errorf("type %d (%s): hash differs across sessions", i, a.tab.TypeString(tys[i], nil))
		}
	}
	empty := loadSession("")
	if _, err := DecodeTypes(empty.env, enc); !errors.Is(err, ErrStale) {
		t.Errorf("decode without the named items: %v, want ErrStale", err)
	}
}

// TestDecodeInterfaceInto: the interface decodes into a registered module
// with no items; a module with another path, or with other imports, is
// ErrStale and leaves no item behind; one that has items is refused.
func TestDecodeInterfaceInto(t *testing.T) {
	a, f := ifaceSession(false)
	m := geoInterface(a, f)
	enc := EncodeInterface(a.env, a.h, m)
	b := loadSession(bodyText)
	wrong := &types.Module{Path: "other"}
	b.items.AddModule(wrong)
	if _, err := DecodeInterfaceInto(b.env, enc, wrong); !errors.Is(err, ErrStale) {
		t.Errorf("wrong path: %v, want ErrStale", err)
	}
	if _, ok := b.items.ByKey(types.ItemKey("geo", "Point")); ok {
		t.Error("a failed decode left an item")
	}
	into := &types.Module{Path: "geo"}
	b.items.AddModule(into)
	got, err := DecodeInterfaceInto(b.env, enc, into)
	if err != nil {
		t.Fatal(err)
	}
	if got.Module != into || len(into.Items) != len(m.Items) || into.Path != "geo" {
		t.Errorf("decoded into %+v, want the registered module with %d items", got.Module, len(m.Items))
	}
	if _, err := DecodeInterfaceInto(b.env, enc, into); err == nil {
		t.Error("decode into a module with items: no error")
	}
}
