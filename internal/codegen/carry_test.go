// Why: the carry intrinsics of std/raw/carry.mk (intrinsic_carry.go) carry
// multi-limb arithmetic, so every result bit and the generated code matter.
// What: TestCarryNative checks each result field of AddCarry64 and
// SubBorrow64 natively on x86_64-windows over edge limbs and every carry in
// (above 1 included, where eval and the two-step lowering must agree);
// TestCarryChainBranchFree reads x86_64-linux's object.
// How: a four-limb add must be one adc chain with no branch at -O2 and no
// branch at -O0, found by disassembling the function.

package codegen

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// carryTestOp is one carry intrinsic: its name and its result's fields.
type carryTestOp struct{ name, val, flag string }

var carryTestOps = []carryTestOp{{"AddCarry64", "sum", "carry"}, {"SubBorrow64", "diff", "borrow"}}

// carryTestRec is the record (val: u64, flag: u64) and the index of each
// field in it (records are in name order).
func carryTestRec(e *testEnv, op carryTestOp) (types.TypeID, map[string]uint32) {
	rec := e.tt.Record([]types.RecordField{{Name: e.in.Intern(op.val), Type: types.U64}, {Name: e.in.Intern(op.flag), Type: types.U64}})
	idx := map[string]uint32{}
	for i, f := range e.tt.Get(rec).Fields {
		idx[e.in.Str(f.Name)] = uint32(i)
	}
	return rec, idx
}

// carryTestBodies are, for each op, <name>Val and <name>Flag(x, y, c: u64):
// u64 returning that field of the call's result, and Add4(a0, a1, a2, a3,
// b0, b1, b2, b3: u64): u64, a four-limb AddCarry64 chain whose limbs and
// last carry are xor-ed; with the intrinsics' items.
func carryTestBodies(e *testEnv) ([]*mir.Body, []types.ItemID) {
	u64s := func(n int) []types.TypeID {
		ps := make([]types.TypeID, n)
		for i := range ps {
			ps[i] = types.U64
		}
		return ps
	}
	var bodies []*mir.Body
	var uses []types.ItemID
	var addID types.ItemID
	var addRec types.TypeID
	var addIdx map[string]uint32
	for _, op := range carryTestOps {
		rec, idx := carryTestRec(e, op)
		id := e.stdFn("std.raw", "", op.name, 0, u64s(3), rec)
		uses = append(uses, id)
		if op.name == "AddCarry64" {
			addID, addRec, addIdx = id, rec, idx
		}
		for _, field := range []string{op.val, op.flag} {
			suffix := "Val"
			if field == op.flag {
				suffix = "Flag"
			}
			f := e.body(e.fn(op.name+suffix, u64s(3), types.U64, 0))
			r := f.call(id, nil, rec, mir.Copy(place(1)), mir.Copy(place(2)), mir.Copy(place(3)))
			fp := f.b.Place(r, mir.Projection{Kind: mir.ProjField, A: idx[field], Type: types.U64})
			f.assign(place(0), use(mir.Copy(fp)), f.at(21, 1))
			f.ret(f.at(21, 1))
			bodies = append(bodies, f.b)
		}
	}
	f := e.body(e.fn("Add4", u64s(8), types.U64, 0))
	carry := mir.Imm(types.U64, 0)
	acc := f.local(types.U64)
	f.assign(place(acc), use(mir.Imm(types.U64, 0)), f.at(22, 1))
	for i := 0; i < 4; i++ {
		r := f.call(addID, nil, addRec, mir.Copy(place(mir.LocalID(1+i))), mir.Copy(place(mir.LocalID(5+i))), carry)
		sum := f.b.Place(r, mir.Projection{Kind: mir.ProjField, A: addIdx["sum"], Type: types.U64})
		next := f.local(types.U64)
		f.assign(place(next), bin(mir.BitXor, mir.Copy(place(acc)), mir.Copy(sum)), f.at(22, 1))
		acc = next
		carry = mir.Copy(f.b.Place(r, mir.Projection{Kind: mir.ProjField, A: addIdx["carry"], Type: types.U64}))
	}
	f.assign(place(0), bin(mir.BitXor, mir.Copy(place(acc)), carry), f.at(22, 1))
	f.ret(f.at(22, 1))
	bodies = append(bodies, f.b)
	return bodies, uses
}

// carryTestUnit is the unit of carryTestBodies with the intrinsics listed.
func carryTestUnit(e *testEnv) *Unit {
	bodies, uses := carryTestBodies(e)
	u := unitOf(e, bodies)
	for _, id := range uses {
		u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(id).Key.String(), Item: id, Pack: -1})
	}
	return u
}

// carryTestWant is the definition: x + y + c with carry 1 when it is 2^64
// or more, x - y - c modulo 2^64 with borrow 1 when x < y + c.
func carryTestWant(op string, x, y, c uint64) (uint64, uint64) {
	two64 := new(big.Int).Lsh(big.NewInt(1), 64)
	bx, by, bc := new(big.Int).SetUint64(x), new(big.Int).SetUint64(y), new(big.Int).SetUint64(c)
	if op == "AddCarry64" {
		s := new(big.Int).Add(new(big.Int).Add(bx, by), bc)
		k := uint64(0)
		if s.Cmp(two64) >= 0 {
			k = 1
		}
		return new(big.Int).Mod(s, two64).Uint64(), k
	}
	d := new(big.Int).Sub(bx, new(big.Int).Add(by, bc))
	b := uint64(0)
	if d.Sign() < 0 {
		b = 1
	}
	return new(big.Int).Mod(d, two64).Uint64(), b
}

func TestCarryNative(t *testing.T) {
	target := types.X86_64Windows
	for _, prof := range []struct {
		name string
		p    types.Profile
		opt  string
	}{{"dev", types.Dev, "-O0"}, {"release", types.Release, "-O2"}} {
		t.Run(prof.name, func(t *testing.T) {
			needLLVM(t)
			e := newTestEnv()
			ll := emit(t, e, carryTestUnit(e), target, prof.p)
			dir := t.TempDir()
			objs := []string{clangC(t, target, dir, "support", supportLL(t, target), "-O2"), clangC(t, target, dir, "pack", ll, prof.opt)}
			edges := []uint64{0, 1, 2, 1<<63 - 1, 1 << 63, ^uint64(0) - 1, ^uint64(0)}
			var decls, body, want strings.Builder
			n := 0
			for _, op := range carryTestOps {
				for _, suffix := range []string{"Val", "Flag"} {
					fmt.Fprintf(&decls, "declare i64 @mk.main.%s%s(i64, i64, i64)\n", op.name, suffix)
				}
				for _, x := range edges {
					for _, y := range edges {
						for _, c := range edges {
							v, fl := carryTestWant(op.name, x, y, c)
							for i, suffix := range []string{"Val", "Flag"} {
								fmt.Fprintf(&body, "  %%r%d = call i64 @mk.main.%s%s(i64 %d, i64 %d, i64 %d)\n  call void @h_num(i64 %%r%d)\n  call void @h_write(ptr @s.nl, i64 1)\n",
									n, op.name, suffix, int64(x), int64(y), int64(c), n)
								w := v
								if i == 1 {
									w = fl
								}
								want.WriteString(strconv.FormatInt(int64(w), 10) + "\n")
								n++
							}
						}
					}
				}
			}
			osIR, kernel := harnessOS(target)
			h := osIR + harnessRT + decls.String() + "\ndefine void @\"mk.std.task.Start\"(ptr %sp) noreturn nounwind {\n" + body.String() +
				"  call void @h_exit(i32 0)\n  unreachable\n}\n"
			exe := linkExe(t, target, dir, append(objs, clangC(t, target, dir, "harness", h, "-O0")), linkOpts{kernel: kernel})
			out, code := runExe(t, target, exe, "")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			got, exp := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), strings.Split(want.String(), "\n")
			for i := range exp {
				if i >= len(got) || got[i] != exp[i] {
					g := "(missing)"
					if i < len(got) {
						g = got[i]
					}
					t.Fatalf("line %d: got %s, want %s", i+1, g, exp[i])
				}
			}
		})
	}
}

func TestCarryChainBranchFree(t *testing.T) {
	bin := needLLVM(t)
	target := types.X86_64Linux
	e := newTestEnv()
	ll := emit(t, e, carryTestUnit(e), target, types.Release)
	if !strings.Contains(ll, "@llvm.uadd.with.overflow.i64") || !strings.Contains(ll, "@llvm.usub.with.overflow.i64") {
		t.Fatal("the carry intrinsics are not lowered to with.overflow calls")
	}
	dir := t.TempDir()
	for _, opt := range []string{"-O0", "-O2"} {
		obj := clangC(t, target, dir, "o"+opt[1:], ll, opt)
		dis := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", obj)
		add4 := carryTestFunction(dis, "mk.main.Add4")
		if add4 == "" {
			t.Fatalf("%s: no Add4 in the disassembly:\n%s", opt, dis)
		}
		for _, ln := range strings.Split(add4, "\n") {
			f := strings.Fields(ln)
			if len(f) >= 2 && strings.HasPrefix(f[1], "j") {
				t.Errorf("%s: Add4 branches: %s\n%s", opt, strings.TrimSpace(ln), add4)
			}
		}
		if opt == "-O2" {
			if n := objMnemonics(add4, []string{"adcq"})["adcq"]; n != 3 {
				t.Errorf("-O2: Add4 has %d adcq, want 3 (one add, then one chain):\n%s", n, add4)
			}
		}
	}
}

// carryTestFunction is the disassembly of the function sym in dis, "" if none.
func carryTestFunction(dis, sym string) string {
	start := strings.Index(dis, "<"+sym+">:")
	if start < 0 {
		return ""
	}
	rest := dis[start:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
