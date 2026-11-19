// Why: slicing a constant temporary for a call should copy nothing, which
// matters for crypto tables.
// What: TestPromoteConstLocal (promote.go: a temporary that only ever holds
// one constant aggregate is that constant's global) and
// TestRawWriteKeepsTheLocal.
// How: any write, exclusive borrow, second constant or call destination
// keeps the local's own memory, and so does a user's local or a raw pointer
// made from one; the IR is checked for the copies and allocas.

package codegen

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func TestPromoteConstLocal(t *testing.T) {
	e := newTestEnv()
	const n = 64 // 512 bytes, past any copy LLVM would scalarize
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, n)}, types.Int)
	tab := func(seed uint64) types.ConstID {
		b := make([]byte, 8*n)
		for i := range n {
			binary.LittleEndian.PutUint64(b[8*i:], seed*uint64(i)+1)
		}
		return e.consts.Intern(types.ConstValue{Type: arr, Align: 8, Bytes: b})
	}
	c1, c2 := tab(3), tab(5)
	sl, msl := e.tt.Slice(types.Int), e.tt.MutSlice(types.Int)
	// Sum(xs: []int, k: int): int and Fill(xs: mut []int): bodiless callees.
	sum := e.fn("Sum", []types.TypeID{sl, types.Int}, types.Int, 0)
	fill := e.fn("Fill", []types.TypeID{msl}, types.Int, 0)
	mk := e.fn("Make", nil, arr, 0)
	sum64 := e.fn("Sum64", []types.TypeID{e.tt.Slice(types.I64), types.Int}, types.Int, 0)
	call := func(f *fnb, callee types.ItemID, arg mir.LocalID, k bool, dest mir.LocalID) {
		args := []mir.Arg{{Operand: mir.Copy(place(arg))}}
		if k {
			args = append(args, mir.Arg{Operand: mir.Copy(place(1))})
		}
		next := f.newBlock()
		f.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: callee}},
			Args: f.b.AddArgs(args...), Dest: place(dest), Target: next, Spawn: mir.NoLocal, Span: f.at(3, 3)})
		f.cur = next
	}
	slice := func(f *fnb, of mir.LocalID, k mir.BorrowKind, st types.TypeID) mir.LocalID {
		s := f.local(st)
		f.assign(place(s), mir.Rvalue{Kind: mir.RvSlice, Borrow: k, Place: place(of), Type: st}, f.at(2, 9))
		return s
	}
	// A Cell-like array type: a shared borrow of it may write its bytes.
	cellArr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, n)}, types.I64)
	e.noFreeze[cellArr] = true
	c3 := e.consts.Intern(types.ConstValue{Type: cellArr, Align: 8, Bytes: make([]byte, 8*n)})
	// name(k: int): int { t := c1; [twin's change]; return Sum(t[..], k) }
	build := func(name string, twin func(f *fnb, t mir.LocalID)) *mir.Body {
		f := e.body(e.fn(name, []types.TypeID{types.Int}, types.Int, 0))
		tl := f.local(arr)
		f.assign(place(tl), use(mir.Const(arr, c1)), f.at(2, 5))
		if twin != nil {
			twin(f, tl)
		}
		call(f, sum, slice(f, tl, mir.Shared, sl), true, 0)
		f.ret(f.at(4, 3))
		return f.b
	}
	bodies := []*mir.Body{
		build("Promoted", nil),
		build("Again", func(f *fnb, tl mir.LocalID) { // the same constant twice: still promoted
			f.assign(place(tl), use(mir.Const(arr, c1)), f.at(2, 20))
		}),
		build("TwoConsts", func(f *fnb, tl mir.LocalID) {
			f.assign(place(tl), use(mir.Const(arr, c2)), f.at(2, 20))
		}),
		build("Written", func(f *fnb, tl mir.LocalID) { // t[k] = 7
			f.assign(f.b.Place(tl, mir.Projection{Kind: mir.ProjIndex, A: 1, Type: types.Int}), use(mir.Imm(types.Int, 7)), f.at(2, 20))
		}),
		build("MutSliced", func(f *fnb, tl mir.LocalID) { // Fill(mut t[..])
			r := f.local(types.Int)
			call(f, fill, slice(f, tl, mir.Exclusive, msl), false, r)
		}),
		build("CallDest", func(f *fnb, tl mir.LocalID) { // t = Make()
			next := f.newBlock()
			f.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: mk}},
				Dest: place(tl), Target: next, Spawn: mir.NoLocal, Span: f.at(2, 20)})
			f.cur = next
		}),
	}
	{ // NotFreeze(k: int): int { t := c3; return Sum(t[..], k) }, t's type not Freeze
		f := e.body(e.fn("NotFreeze", []types.TypeID{types.Int}, types.Int, 0))
		tl := f.local(cellArr)
		f.assign(place(tl), use(mir.Const(cellArr, c3)), f.at(2, 5))
		call(f, sum64, slice(f, tl, mir.Shared, e.tt.Slice(types.I64)), true, 0)
		f.ret(f.at(4, 3))
		bodies = append(bodies, f.b)
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		ll := emit(t, e, unitOf(e, bodies, sum, fill, mk, sum64), types.X86_64Windows, prof)
		copyOf := regexp.MustCompile(`call void @llvm\.memcpy[^(]*\(ptr %v[0-9]+, ptr @mk\.c\.[0-9]+, i64 512`)
		for _, name := range []string{"Promoted", "Again"} {
			fn := funcIR(t, ll, "mk.main."+name)
			// The constant's 512 bytes are never copied; the 16-byte memcpy
			// of the slice argument (Win64 Indirect) is not that.
			if copyOf.MatchString(fn) || strings.Contains(fn, "alloca [512 x i8]") {
				t.Errorf("%v %s: the constant is copied into the frame:\n%s", prof, name, fn)
			}
			if !regexp.MustCompile(`getelementptr inbounds nuw i8, ptr @mk\.c\.[0-9]+`).MatchString(fn) {
				t.Errorf("%v %s: the slice does not point into the constant's global:\n%s", prof, name, fn)
			}
		}
		for _, name := range []string{"TwoConsts", "Written", "MutSliced", "CallDest", "NotFreeze"} {
			fn := funcIR(t, ll, "mk.main."+name)
			if name != "CallDest" && !copyOf.MatchString(fn) || !strings.Contains(fn, "alloca [512 x i8]") {
				t.Errorf("%v %s: promoted, want the local's own copy:\n%s", prof, name, fn)
			}
		}
	}
	for _, target := range types.Targets() {
		clangC(t, target, t.TempDir(), "promote", emit(t, e, unitOf(e, bodies, sum, fill, mk, sum64), target, types.Release), "-O2")
	}
}

// TestRawWriteKeepsTheLocal: a raw write that the language defines (no
// live loan covers the place) must land in the local's own memory, so
// constLocals never binds such a local to the constant's read-only global.
// A user's local (`var t := Tab`) is never promoted, even when the raw
// pointer is made in a callee (UserSum, UserStash); a temporary is not
// promoted when a reference made from it becomes a raw pointer in the body
// (TempOf, TempData, TempLeak). A temporary only sliced for a call still is
// (Mixed's second local), and so is one whose call's integer result reaches
// a value that is not Freeze (TempSumWrap: the walk does not follow
// scalars). The run checks, on Windows and in WSL, at -O0 and -O2, that
// each write is seen: 99, where a promoted local faults on .rdata or reads
// the constant's 1.
func TestRawWriteKeepsTheLocal(t *testing.T) {
	e := newTestEnv()
	const n = 64
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, n)}, types.Int)
	b := make([]byte, 8*n)
	for i := range n {
		binary.LittleEndian.PutUint64(b[8*i:], 3*uint64(i)+1)
	}
	c1 := e.consts.Intern(types.ConstValue{Type: arr, Align: 8, Bytes: b})
	sl, ri, pi := e.tt.Slice(types.Int), e.tt.Ref(types.Int), e.tt.RawPtr(types.Int)
	p0 := e.tt.Param(0)
	rawOf := e.stdFn("std.raw", "", "Of", 1, []types.TypeID{e.tt.Ref(p0)}, e.tt.RawPtr(p0))
	rawData := e.stdFn("std.raw", "", "Data", 1, []types.TypeID{e.tt.Slice(p0)}, e.tt.RawPtr(p0))
	rawWrite := e.stdFn("std.raw", "", "Write", 1, []types.TypeID{e.tt.RawPtr(p0), p0}, types.Void)
	ti := []types.GenericArg{types.TypeArg(types.Int)}
	sum := e.fn("Sum", []types.TypeID{sl, types.Int}, types.Int, 0)
	leak := e.fn("Leak", []types.TypeID{ri}, pi, 0) // Leak(r: &int): *int, a raw.Of in another module
	stash := e.fn("Stash", []types.TypeID{ri}, types.Void, 0)
	flush := e.fn("Flush", nil, types.Void, 0)
	cellT := e.structT("CellLike", types.Int)
	e.noFreeze[cellT] = true
	wrap := e.fn("Wrap", []types.TypeID{types.Int}, cellT, 0)
	at := func(f *fnb, l mir.LocalID) mir.Place { // l[k]
		return f.b.Place(l, mir.Projection{Kind: mir.ProjIndex, A: 1, Type: types.Int})
	}
	ref := func(f *fnb, p mir.Place) mir.LocalID { // &p
		r := f.local(ri)
		f.assign(place(r), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: p, Type: ri}, f.at(2, 9))
		return r
	}
	slice := func(f *fnb, l mir.LocalID) mir.LocalID { // l[..]
		s := f.local(sl)
		f.assign(place(s), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: place(l), Type: sl}, f.at(2, 11))
		return s
	}
	// name(k: int): int { t := c1 (a temporary, or `var t` when user);
	// [twin's change]; return t[k] }
	build := func(name string, user bool, twin func(f *fnb, tl mir.LocalID)) *mir.Body {
		f := e.body(e.fn(name, []types.TypeID{types.Int}, types.Int, 0))
		tl := f.local(arr)
		if user {
			f.b.Locals[tl].Kind, f.b.Locals[tl].Var, f.b.Locals[tl].Name = mir.LocalUser, true, e.in.Intern("t")
		}
		f.assign(place(tl), use(mir.Const(arr, c1)), f.at(2, 5))
		twin(f, tl)
		f.assign(place(0), use(mir.Copy(at(f, tl))), f.at(3, 3))
		f.ret(f.at(4, 3))
		return f.b
	}
	poke := func(f *fnb, p mir.LocalID) {
		f.call(rawWrite, ti, types.Void, mir.Copy(place(p)), mir.Imm(types.Int, 99))
	}
	callSum := func(f *fnb, s mir.LocalID) {
		f.callTo(direct(sum), place(f.local(types.Int)), mir.NoLocal, f.at(2, 20), mir.Copy(place(s)), mir.Copy(place(1)))
	}
	bodies := []*mir.Body{
		// var t := Tab; Sum(t[..], k): Sum's module may make a raw pointer
		// from the slice and keep it past the call.
		build("UserSum", true, func(f *fnb, tl mir.LocalID) { callSum(f, slice(f, tl)) }),
		// var t := Tab; Stash(&t[k]); Flush(): Flush writes 99 through the
		// pointer Stash kept, after the loan of &t[k] ended.
		build("UserStash", true, func(f *fnb, tl mir.LocalID) {
			f.callTo(direct(stash), place(f.local(types.Void)), mir.NoLocal, f.at(2, 20), mir.Copy(place(ref(f, at(f, tl)))))
			f.callTo(direct(flush), place(f.local(types.Void)), mir.NoLocal, f.at(2, 30))
		}),
		// var t := Tab; unsafe { raw.Write(raw.Of(&t[k]), 99) }: the raw
		// write case.
		build("UserOf", true, func(f *fnb, tl mir.LocalID) {
			poke(f, f.call(rawOf, ti, pi, mir.Copy(place(ref(f, at(f, tl))))))
		}),
		// The same on a temporary, the raw pointer made in the body by
		// raw.Of, by raw.Data and by a callee's *int result.
		build("TempOf", false, func(f *fnb, tl mir.LocalID) {
			poke(f, f.call(rawOf, ti, pi, mir.Copy(place(ref(f, at(f, tl))))))
		}),
		build("TempData", false, func(f *fnb, tl mir.LocalID) {
			s := slice(f, tl)
			poke(f, f.call(rawData, ti, pi, mir.Copy(place(s))))
			callSum(f, s)
		}),
		build("TempLeak", false, func(f *fnb, tl mir.LocalID) {
			p := f.local(pi)
			f.callTo(direct(leak), place(p), mir.NoLocal, f.at(2, 20), mir.Copy(place(ref(f, at(f, tl)))))
			poke(f, p)
		}),
		// A user's local and a temporary of one constant, both only sliced
		// for Sum: the temporary is promoted, the user's local is not.
		build("Mixed", true, func(f *fnb, tl mir.LocalID) {
			callSum(f, slice(f, tl))
			tmp := f.local(arr)
			f.assign(place(tmp), use(mir.Const(arr, c1)), f.at(2, 40))
			callSum(f, slice(f, tmp))
		}),
	}
	bodiless := []types.ItemID{sum, leak, stash, flush}
	// x := Sum(t[..], k); Wrap(x): an integer result carries no pointer, so
	// Wrap's result (not Freeze) is not derived from t: still promoted.
	// IR only (the run's harness has no Wrap).
	irBodies := append(append([]*mir.Body(nil), bodies...), build("TempSumWrap", false, func(f *fnb, tl mir.LocalID) {
		x := f.local(types.Int)
		f.callTo(direct(sum), place(x), mir.NoLocal, f.at(2, 20), mir.Copy(place(slice(f, tl))), mir.Copy(place(1)))
		f.callTo(direct(wrap), place(f.local(cellT)), mir.NoLocal, f.at(2, 30), mir.Copy(place(x)))
	}))
	irBodiless := append(append([]types.ItemID(nil), bodiless...), wrap)
	copyOf := regexp.MustCompile(`call void @llvm\.memcpy[^(]*\(ptr %v[0-9]+, ptr @mk\.c\.[0-9]+, i64 512`)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		ll := emit(t, e, unitOf(e, irBodies, irBodiless...), types.X86_64Windows, prof)
		for _, name := range []string{"UserSum", "UserStash", "UserOf", "TempOf", "TempData", "TempLeak"} {
			fn := funcIR(t, ll, "mk.main."+name)
			if !copyOf.MatchString(fn) || !strings.Contains(fn, "alloca [512 x i8]") {
				t.Errorf("%v %s: bound to the constant's global, want the local's own copy:\n%s", prof, name, fn)
			}
		}
		if fn := funcIR(t, ll, "mk.main.TempSumWrap"); copyOf.MatchString(fn) || strings.Contains(fn, "alloca [512 x i8]") {
			t.Errorf("%v TempSumWrap: the constant is copied into the frame:\n%s", prof, fn)
		}
		fn := funcIR(t, ll, "mk.main.Mixed")
		if c, a := len(copyOf.FindAllString(fn, -1)), strings.Count(fn, "alloca [512 x i8]"); c != 1 || a != 1 {
			t.Errorf("%v Mixed: %d copies and %d allocas of the constant, want 1 and 1 (the user's local only):\n%s", prof, c, a, fn)
		}
	}
	for _, target := range types.Targets() {
		clangC(t, target, t.TempDir(), "pcg1", emit(t, e, unitOf(e, irBodies, irBodiless...), target, types.Release), "-O2")
	}
	// The run. The harness defines Stash (keeps its pointer), Flush (writes
	// 99 through it), Leak (returns its pointer) and Sum (returns 0).
	runNames := []string{"UserStash", "UserOf", "TempOf", "TempData", "TempLeak"}
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				var body, want strings.Builder
				decls := ""
				for i, name := range runNames {
					decls += "declare i64 @mk.main." + name + "(i64)\n"
					fmt.Fprintf(&body, "  %%r%d = call i64 @mk.main.%s(i64 0)\n  call void @h_num(i64 %%r%d)\n  call void @h_write(ptr @s.nl, i64 1)\n", i, name, i)
					want.WriteString("99\n")
				}
				sumDef := "define i64 @mk.main.Sum(ptr %d, i64 %n, i64 %k) nounwind {\n  ret i64 0\n}\n"
				if target.OS == types.OSWindows { // Win64 passes a 16-byte slice by reference
					sumDef = "define i64 @mk.main.Sum(ptr %s, i64 %k) nounwind {\n  ret i64 0\n}\n"
				}
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				h := osIR + harnessRT + decls + sumDef + `
@kept = internal global ptr null

define void @mk.main.Stash(ptr %p) nounwind {
  store ptr %p, ptr @kept, align 8
  ret void
}

define void @mk.main.Flush() nounwind {
  %p = load ptr, ptr @kept, align 8
  store i64 99, ptr %p, align 8
  ret void
}

define ptr @mk.main.Leak(ptr %p) nounwind {
  ret ptr %p
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + body.String() + `  call void @h_exit(i32 0)
  unreachable
}
`
				objs := []string{clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt), clangC(t, target, dir, "harness", h, "-O0")}
				got, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				if code != 0 || got != want.String() {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, got, want.String())
				}
			})
		}
	}
}
