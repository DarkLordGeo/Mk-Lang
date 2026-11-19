// Why: Format on an error-set value inside an instance must become a switch
// over the set's members, since each member has its own provided Format.
// What: TestInstanceSwitch and TestIsFormat over errFmtWorld.
// How: errFmtCore stands in for std.core; the test collects Show[LoadError]
// and inspects the instance body.

package mono

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/check"
	"mk/internal/iface"
	"mk/internal/lower"
	"mk/internal/mir"
	"mk/internal/resolve"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// errFmtCore is a std.core stand-in with what a provided Format needs.
const errFmtCore = `export interface Format {
    Format(&self, w: mut Writer): WriteError!void
}

export error WriteError

export view struct Writer {
    n: int
}

export Writer.Str(mut self, s: str): WriteError!void {
}

impl Format for int {
    Format(&self, w: mut Writer): WriteError!void {
    }
}

export error NotFound(id: int)
export error Denied

export type LoadError = NotFound | Denied

export Show[T: Format](w: mut Writer, x: T): WriteError!void {
    try x.Format(w)
}

export Run(w: mut Writer, e: LoadError): WriteError!void {
    try Show(w, e)
}
`

// errFmtWorld is newSrcWorldFor over errFmtCore as std.core, the prelude
// module, so resolve declares the provided impls, with the Format impls in
// the fake env's table as the driver's monoEnv answers them.
func errFmtWorld(t *testing.T) (*srcWorld, *types.KnownItems) {
	t.Helper()
	w := &world{in: source.NewInterner(), items: types.NewItems(), consts: types.NewConstTable(),
		bodies: map[types.ItemID]*mir.Body{}, impls: map[implKey]types.ItemID{}, onerr: map[onerrKey]bool{}}
	w.tab = types.NewTable(w.in, w.items, w.consts)
	w.h = iface.NewHasher(w.tab, w.consts, func(id types.ItemID) *mir.Body { return w.bodies[id] })
	sw := &srcWorld{world: w, sm: source.NewSourceMap(), path: "std.core"}
	file := sw.sm.Add("std/core/00.mk", []byte(errFmtCore))
	f, ds := syntax.ParseFile(file, w.in, syntax.Options{AllowBodiless: true})
	sw.fail(t, "parse", ds)
	w.mod = w.items.AddModule(&types.Module{Path: "std.core", Files: []source.FileID{file.ID}, Std: true, UnsafePermitted: true})
	rs := resolve.NewSession(&resolve.Config{Interner: w.in, Sources: sw.sm, Items: w.items, PreludePath: "std.core"})
	rm := rs.Begin(&resolve.ModuleInput{Module: w.mod, Files: []*ast.File{f}, Imports: map[string]types.ModuleID{}})
	info := rm.Finish(1)
	sw.fail(t, "resolve", info.Diags)
	rds, gds := rm.Settle()
	sw.fail(t, "resolve", append(rds, gds...))
	k := &types.KnownItems{Format: sw.item(t, "Format"), Writer: sw.item(t, "Writer")}
	c := check.New(&check.Config{Interner: w.in, Types: w.tab, Items: w.items, Consts: w.consts, Target: types.X86_64Windows, Known: k})
	sw.checker = c
	m := c.Signatures(&check.ModuleInput{Module: w.mod, Files: []*ast.File{f}, Info: info})
	sw.fail(t, "signatures", m.Diags)
	lcfg := &lower.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Validate: true, Sources: sw.sm, Impls: lower.ImplsOf(c), Known: k}
	for _, id := range m.Units() {
		it := w.items.Get(id)
		body := c.Check(m, id)
		sw.fail(t, "check "+it.Key.String(), body.Diags)
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { mb = lower.Lower(lcfg, lower.FromCheck(lcfg, c, body)) }); ie != nil {
			t.Fatalf("lower ICE in %s: %v", it.Key, ie)
		}
		w.bodies[id] = mb
		sw.units = append(sw.units, id)
	}
	for _, name := range []string{"NotFound", "Denied"} {
		e := sw.item(t, name)
		et := w.tab.ErrorSetType(w.tab.ErrorSet([]types.ErrorMember{{Item: e}}))
		impl, ok := c.ImplOf(k.Format, nil, et, &types.ReadSet{})
		if !ok || impl == types.NoItem {
			t.Fatalf("%s has no provided Format", name)
		}
		meth, _ := w.items.ByKey(types.ImplMemberKey(w.items.Get(impl).Key, "Format"))
		w.impls[implKey{k.Format, 0, et}] = meth
	}
	return sw, k
}

// TestInstanceSwitch: Show[LoadError]'s Format call is a switch with one
// direct call of each member's provided Format, and validates.
func TestInstanceSwitch(t *testing.T) {
	sw, k := errFmtWorld(t)
	in, ds := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Run")}}, Options{Known: k, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds[0].Message)
	}
	shows := instancesOf(in, sw.item(t, "Show"))
	if len(shows) != 1 {
		t.Fatalf("%d Show instances", len(shows))
	}
	b := shows[0].Body
	calls, switches := map[types.ItemID]bool{}, 0
	for _, blk := range b.Blocks {
		switch tm := blk.Term; {
		case tm.Kind == mir.TermCall && tm.Callee.Kind == mir.CallInterface:
			t.Fatal("an interface call survived")
		case tm.Kind == mir.TermCall:
			calls[tm.Callee.Fn.Item] = true
		case tm.Kind == mir.TermSwitch:
			switches++
		}
	}
	if switches != 1 || len(calls) != 2 {
		t.Fatalf("switches %d, called %d functions; want 1 and both members' Format", switches, len(calls))
	}
	for _, name := range []string{"NotFound", "Denied"} {
		e := sw.item(t, name)
		meth := sw.impls[implKey{k.Format, 0, sw.tab.ErrorSetType(sw.tab.ErrorSet([]types.ErrorMember{{Item: e}}))}]
		if !calls[meth] {
			t.Errorf("no call of %s's Format", name)
		}
	}
}

// TestIsFormat: a set is Format when each member is.
func TestIsFormat(t *testing.T) {
	sw, k := errFmtWorld(t)
	bl := &builder{env: sw, tab: sw.tab, opt: Options{Known: k}}
	set := sw.tab.ErrorSetType(sw.tab.ErrorSet([]types.ErrorMember{{Item: sw.item(t, "NotFound")}, {Item: sw.item(t, "Denied")}}))
	if !bl.isFormat(set) {
		t.Error("NotFound | Denied is not Format")
	}
	delete(sw.impls, implKey{k.Format, 0, sw.tab.ErrorSetType(sw.tab.ErrorSet([]types.ErrorMember{{Item: sw.item(t, "Denied")}}))})
	if bl.isFormat(set) {
		t.Error("a set with a member without Format is Format")
	}
}
