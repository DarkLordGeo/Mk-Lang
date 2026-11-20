// Why: std.rt's Finish writes only `error: <display name>`, which loses the
// fields of an error Main returns; a failing program should print the whole
// error.
//
// What: mainEntry and mainReport, which build a wrapper of Main as MIR
// (`<root>.Main#report`, as the test runner's Main is built in
// testrunner.go), frameNames (the backtrace names codegen gives the
// compiler-made functions) and errorCodeOf.
//
// How: the wrapper reports an error as `error: `, its display name's module
// prefix, then the error as its `{e}` hole formats it (`error:
// main.Bad(code: 7)`); an error with no fields prints `error: <display
// name>`, as std.rt's Finish writes it. codegen's CallMain calls the wrapper
// (codegen.Config.Main) in a hosted bin whose Main can fail. mk test's
// runner Main is wrapped the same way, so a failing test's error prints so
// too.

package driver

import (
	"mk/internal/codegen"
	"mk/internal/eval"
	"mk/internal/lower"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// mainReportName is the wrapper's name inside the root module's key:
// `<root>.Main#report`, which no source can spell, so it never collides
// with a user item.
const mainReportName = "Main#report"

// mainEntry is the function a hosted bin with root module mainMod enters
// through CallMain: Main, or for a Main that can fail the report wrapper
// (made on first use, then found by key). NoItem when the module has no
// Main (a corpus unit). A freestanding bin, a std without
// std.rt.MainErrorWritten or System.Stderr, and an error with no Format
// impl keep Main itself, whose error Finish names.
func (w *world) mainEntry(mainMod string) types.ItemID {
	mainID, ok := w.items.ByKey(types.ItemKey(mainMod, "Main"))
	if !ok {
		return types.NoItem
	}
	fd, ok := w.items.Get(mainID).Decl.(*types.FuncDecl)
	if !ok || fd.Sig.Errors == types.EmptyErrors || isFree(w.cfg.Target) || w.sem5 == nil {
		return mainID
	}
	w.reportMu.Lock()
	defer w.reportMu.Unlock()
	key := types.ItemKey(mainMod, mainReportName)
	if id, ok := w.items.ByKey(key); ok {
		return id
	}
	if id, ok := w.mainReport(mainID, key); ok {
		return id
	}
	return mainID
}

// mainReport adds the wrapper of Main under key and stores its body; false
// (nothing added) when std lacks a piece it calls. As MIR:
//
//	r := Main(sys)
//	return ok(r)                         // on success
//	e:                                   // the error edge
//	w := sys.Stderr()
//	switch e { case Ej: w.Str("error: <prefix of Ej>"); Ej.Format(&ej, mut w) }
//	w.Str("\n"); std.rt.MainErrorWritten()
//	return err(e)                        // a failed write skips the line's end
//
// The error is returned as Main returned it, so Finish still writes the
// trace and exits 1; no trace call is added (lower adds those to source
// bodies only). Stderr is buffered and Finish flushes it first, so the line
// follows the program's own output.
func (w *world) mainReport(mainID types.ItemID, key types.Key) (types.ItemID, bool) {
	items, tab := w.items, w.tab
	look := func(k types.Key) (types.ItemID, *types.Signature, bool) {
		id, ok := items.ByKey(k)
		if !ok {
			return types.NoItem, nil, false
		}
		fd, ok := items.Get(id).Decl.(*types.FuncDecl)
		if !ok {
			return types.NoItem, nil, false
		}
		return id, &fd.Sig, true
	}
	_, cmSig, ok1 := look(types.ItemKey("std.rt", "CallMain"))
	written, _, ok2 := look(types.ItemKey("std.rt", "MainErrorWritten"))
	stderr, stderrSig, ok3 := look(types.MemberKey("std.core", "System", "Stderr"))
	str, strSig, ok4 := look(types.MemberKey("std.core", "Writer", "Str"))
	format := w.sem5.known.Format
	if !ok1 || !ok2 || !ok3 || !ok4 || format == types.NoItem || len(cmSig.Params) != 1 || len(strSig.Params) != 2 {
		return types.NoItem, false
	}
	msig := w.declSig(mainID)
	if len(msig.Params) != 1 {
		return types.NoItem, false // check gives every Main (sys: &System)
	}
	// Each member's Format method, its set, and the line's start.
	type member struct {
		item, format types.ItemID
		errs         types.ErrorSetID
		head         string
	}
	env := w.sem5.monoEnv(w)
	var ms []member
	for _, m := range tab.Members(msig.Errors) {
		if m.IsParam {
			return types.NoItem, false
		}
		et := tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: m.Item}}))
		fn, _, ok := env.ImplMethod(format, 0, et)
		if !ok {
			return types.NoItem, false
		}
		it := items.Get(m.Item)
		disp := codegen.ErrorDisplayIn(items, it, w.display) // a dependency by its local name
		prefix := disp[:len(disp)-len(w.in.Str(it.Name))]
		ms = append(ms, member{item: m.Item, format: fn, errs: w.declSig(fn).Errors, head: "error: " + prefix})
	}

	span := msig.Span
	sysT := cmSig.Params[0].Type
	sig := types.Signature{Name: msig.Name, Result: msig.Result, Errors: msig.Errors, Span: span,
		Params: []types.Param{{Name: w.in.Intern("sys"), Type: sysT, Span: span}}}
	mod := items.Get(mainID).Module
	id := items.AddItem(&types.Item{Module: mod, Name: sig.Name, Kind: types.KindFunc, Span: span, Key: key,
		Decl: &types.FuncDecl{Sig: sig}})
	rsig := w.declSig(id)
	rsig.Item = id
	ret := rsig.ReturnType(tab)

	b := mir.NewBody(id, span, ret)
	sys := b.AddParam(sysT, rsig.Params[0].Name, 0)
	temp := func(t types.TypeID) mir.LocalID {
		return b.AddLocal(mir.Local{Type: t, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	}
	block := func() mir.BlockID { return b.AddBlock(mir.OriginBody, mir.NoDefer) }
	assign := func(at mir.BlockID, dest mir.LocalID, rv mir.Rvalue) {
		b.Blocks[at].Stmts = append(b.Blocks[at].Stmts, mir.Statement{Kind: mir.StAssign, Place: b.Place(dest), Rvalue: rv})
	}
	// call ends block at with fn(args) into dest, then next; a fallible
	// fn's error goes to onErr (into a temporary of its set), as a match
	// that ignores it.
	call := func(at mir.BlockID, fn types.ItemID, args []mir.Arg, dest mir.LocalID, next mir.BlockID, errs types.ErrorSetID, onErr mir.BlockID) {
		t := mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn}},
			Args: b.AddArgs(args...), Dest: b.Place(dest), Target: next, Spawn: mir.NoLocal}
		if errs != types.EmptyErrors {
			t.HasErr, t.Err = true, mir.ErrorEdge{Kind: mir.EdgeMatch, Block: onErr, Error: temp(tab.ErrorSetType(errs))}
		}
		b.Blocks[at].Term = t
	}

	entry, okB, errB, endB, retErr := block(), block(), block(), block(), block()
	res, e := temp(msig.Result), temp(tab.ErrorSetType(msig.Errors))
	call(entry, mainID, []mir.Arg{{Operand: mir.Copy(b.Place(sys))}}, res, okB, types.EmptyErrors, 0)
	b.Blocks[entry].Term.HasErr = true
	b.Blocks[entry].Term.Err = mir.ErrorEdge{Kind: mir.EdgeTry, Block: errB, Error: e}

	ok := mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggOk, Type: ret}
	if msig.Result != types.Void {
		ok.Ops = b.AddOperands(mir.Move(b.Place(res)))
	}
	assign(okB, mir.ReturnLocal, ok)
	b.Blocks[okB].Term = mir.Terminator{Kind: mir.TermReturn, Exit: mir.ExitNormal}

	// The error edge: the writer, then a switch on the error's code.
	writerT := stderrSig.Result
	wr, wm := temp(writerT), temp(tab.Mut(writerT))
	sw := block()
	call(errB, stderr, []mir.Arg{{Operand: mir.Copy(b.Place(sys))}}, wr, sw, types.EmptyErrors, 0)
	assign(sw, wm, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: b.Place(wr), Origin: mir.OriginPlain, Type: tab.Mut(writerT)})
	wArg := mir.Arg{Operand: mir.Copy(b.Place(wm)), Mode: mir.ArgReborrow}
	strArg := func(s string) mir.Arg {
		return mir.Arg{Operand: lower.DataConst(tab, tab.Consts(), types.Str, []byte(s), span)}
	}
	strErrs := strSig.Errors
	d := temp(types.U64)
	assign(sw, d, mir.Rvalue{Kind: mir.RvDiscriminant, Place: b.Place(e), Type: types.U64})
	cases := make([]mir.SwitchCase, len(ms))
	for i, m := range ms {
		head, fmtB := block(), block()
		cases[i] = mir.SwitchCase{Value: uint64(errorCodeOf(items, m.item)), Target: head}
		call(head, str, []mir.Arg{wArg, strArg(m.head)}, temp(types.Void), fmtB, strErrs, retErr)
		// The member's own value (payloads are plain), then &it, as
		// lower's errSetFormat passes it.
		mt := tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: m.item}}))
		dc := mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelCase, A: uint32(m.item), Type: b.Locals[e].Type}
		var ops []mir.Operand
		if ed, ok := items.Get(m.item).Decl.(*types.ErrorDecl); ok {
			for k, f := range ed.Fields {
				ops = append(ops, mir.Copy(b.Place(e, dc, mir.Projection{Kind: mir.ProjField, A: uint32(k), Type: f.Type})))
			}
		}
		v, rv := temp(mt), temp(tab.Ref(mt))
		assign(fmtB, v, mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: m.item, Type: mt, Ops: b.AddOperands(ops...)})
		assign(fmtB, rv, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: b.Place(v), Origin: mir.OriginPlain, Type: tab.Ref(mt)})
		call(fmtB, m.format, []mir.Arg{{Operand: mir.Copy(b.Place(rv))}, wArg}, temp(types.Void), endB, m.errs, retErr)
	}
	un := block()
	b.Blocks[un].Term = mir.Terminator{Kind: mir.TermUnreachable}
	b.Blocks[sw].Term = mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(b.Place(d)), Cases: b.AddCases(cases...), Default: un}
	marked := block()
	call(endB, str, []mir.Arg{wArg, strArg("\n")}, temp(types.Void), marked, strErrs, retErr)
	call(marked, written, nil, temp(types.Void), retErr, types.EmptyErrors, 0)
	assign(retErr, mir.ReturnLocal, mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggErr, Ops: b.AddOperands(mir.Move(b.Place(e))), Type: ret})
	b.Blocks[retErr].Term = mir.Terminator{Kind: mir.TermReturn, Exit: mir.ExitTry}

	menv := &mir.Env{Types: tab, Sig: rsig, Known: &w.sem5.known, Callee: func(id types.ItemID) *types.Signature {
		if fd, ok := items.Get(id).Decl.(*types.FuncDecl); ok {
			return &fd.Sig
		}
		return nil
	}}
	if err := b.Validate(menv); err != nil {
		source.ICEf(span, "driver: Main's report wrapper: %v", err)
	}
	w.sem5.store(id, &bodyEntry{mir: b, state: eval.BodyReady, flowed: true})
	return id, true
}

// frameNames is codegen.Config.Frames for a bin entered through entry
// (cfg.Main, the zero key for Main itself): the compiler-made functions
// a backtrace leaves out (Main's report wrapper; in mk test's runner, its
// Main) and each test clone's `test "name"`. nil when there are none.
func (w *world) frameNames(mainMod string, entry types.Key) map[string]string {
	main := types.ItemKey(mainMod, "Main").String()
	var out map[string]string
	if e := entry.String(); e != "" && e != main {
		out = map[string]string{e: ""}
	}
	if mainMod == testRunnerModule {
		if out == nil {
			out = map[string]string{}
		}
		out[main] = ""
		for k, n := range w.testFrames {
			out[k] = n
		}
	}
	return out
}

// errorCodeOf is error item e's code: its declared one, else its key's
// (lower's errorCode, codegen's errorCode).
func errorCodeOf(items *types.Items, e types.ItemID) uint32 {
	if ed, ok := items.Get(e).Decl.(*types.ErrorDecl); ok && ed.Code != 0 {
		return ed.Code
	}
	return types.ErrorCode(items.Get(e).Key)
}
