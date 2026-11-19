// Why: LLVM's back ends call __udivti3, __umodti3, __divti3 and __modti3 for
// an i128 division, and with no C runtime the support pack must define them.
// What: TestTiAgainstGo (LLVM's own i128 udiv, urem, sdiv and srem
// against math/big), TestTiDivideByZeroTraps, TestTiNoSelfCall (no
// support pack calls a 128-bit routine) and TestTiRowsDiscardable.
// How: tiHarness builds an -O0 IR program over tiPairs' edge values, runs it
// natively and compares its output; the routines live in support_ti.go.

package codegen

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/types"
)

// tiPair is one operand pair of TestTiAgainstGo, two's complement.
type tiPair struct{ a, b [2]uint64 } // [lo, hi]

func (p tiPair) bytes() []byte {
	var out [32]byte
	binary.LittleEndian.PutUint64(out[0:], p.a[0])
	binary.LittleEndian.PutUint64(out[8:], p.a[1])
	binary.LittleEndian.PutUint64(out[16:], p.b[0])
	binary.LittleEndian.PutUint64(out[24:], p.b[1])
	return out[:]
}

// tiBig is x read as unsigned, or as two's complement when signed.
func tiBig(x [2]uint64, signed bool) *big.Int {
	v := new(big.Int).Lsh(new(big.Int).SetUint64(x[1]), 64)
	v.Or(v, new(big.Int).SetUint64(x[0]))
	if signed && x[1]>>63 == 1 {
		v.Sub(v, new(big.Int).Lsh(big.NewInt(1), 128))
	}
	return v
}

// tiBytes is v modulo 2^128, little-endian.
func tiBytes(v *big.Int) []byte {
	mod := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Mod(v, mod)
	var out [16]byte
	lo := new(big.Int).And(u, new(big.Int).SetUint64(^uint64(0)))
	binary.LittleEndian.PutUint64(out[0:], lo.Uint64())
	binary.LittleEndian.PutUint64(out[8:], new(big.Int).Rsh(u, 64).Uint64())
	return out[:]
}

// tiPairs are the edge values crossed with each other (but a zero divisor)
// and random pairs of every magnitude: both halves random, a high half of
// zero (the fast path), divisors a few bits shorter than the dividend (the
// shift loop's short runs), and single-bit values.
func tiPairs() []tiPair {
	edges := [][2]uint64{
		{0, 0}, {1, 0}, {2, 0}, {3, 0}, {7, 0}, {10, 0},
		{1<<63 - 1, 0}, {1 << 63, 0}, {1<<63 + 1, 0}, {^uint64(0), 0}, {^uint64(0) - 1, 0},
		{0, 1}, {1, 1}, {^uint64(0), 1}, {0, 2},
		{^uint64(0), 1<<63 - 1}, {^uint64(0) - 1, 1<<63 - 1}, {0, 1 << 63}, {1, 1 << 63},
		{^uint64(0), ^uint64(0)}, {^uint64(0) - 1, ^uint64(0)}, {^uint64(0) - 9, ^uint64(0)},
		{1 << 63, ^uint64(0)}, {0, ^uint64(0)}, {0x0123456789abcdef, 0xfedcba9876543210},
	}
	var ps []tiPair
	for _, a := range edges {
		for _, b := range edges {
			if b != [2]uint64{} {
				ps = append(ps, tiPair{a, b})
			}
		}
	}
	rng := rand.New(rand.NewSource(882))
	word := func(bits int) [2]uint64 {
		switch {
		case bits <= 0:
			return [2]uint64{}
		case bits <= 64:
			return [2]uint64{rng.Uint64()>>(64-bits) | 1<<(bits-1), 0}
		}
		return [2]uint64{rng.Uint64(), rng.Uint64()>>(128-bits) | 1<<(bits-65)}
	}
	for i := 0; i < 3000; i++ {
		var a, b [2]uint64
		switch i % 6 {
		case 0:
			a, b = [2]uint64{rng.Uint64(), rng.Uint64()}, [2]uint64{rng.Uint64(), rng.Uint64()}
		case 1:
			a, b = word(1+rng.Intn(64)), word(1+rng.Intn(64))
		case 2:
			n := 1 + rng.Intn(128)
			a, b = word(n), word(max(1, n-rng.Intn(8)))
		case 3:
			a, b = word(1+rng.Intn(128)), word(1+rng.Intn(128))
		case 4:
			a, b = word(65+rng.Intn(64)), word(1+rng.Intn(64))
		default:
			n, m := rng.Intn(128), rng.Intn(128)
			a, b = [2]uint64{}, [2]uint64{}
			a[n/64] = 1 << (n % 64)
			b[m/64] = 1 << (m % 64)
		}
		if rng.Intn(4) == 0 { // negative values for the signed forms
			a = [2]uint64{^a[0] + 1, ^a[1]}
			if a[0] == 0 {
				a[1]++
			}
		}
		if b == [2]uint64{} {
			b = [2]uint64{1, 0}
		}
		ps = append(ps, tiPair{a, b})
	}
	return ps
}

// tiHarness is an -O0 program over the n pairs of @ops: for each it does
// udiv, urem, sdiv and srem i128 on values loaded at run time (so LLVM calls
// __udivti3, __umodti3, __divti3 and __modti3) and writes the four results.
func tiHarness(ops []byte, n int) string {
	return fmt.Sprintf(`
@ops = internal global [%d x i8] c"%s", align 16
@out = internal global [%d x i8] zeroinitializer, align 16

define void @"mk.std.task.Start"(ptr %%sp) noreturn nounwind {
entry:
  %%i = alloca i64, align 8
  store i64 0, ptr %%i, align 8
  br label %%loop
loop:
  %%iv = load i64, ptr %%i, align 8
  %%off = mul i64 %%iv, 32
  %%pa = getelementptr inbounds i8, ptr @ops, i64 %%off
  %%pb = getelementptr inbounds i8, ptr %%pa, i64 16
  %%a = load i128, ptr %%pa, align 16
  %%b = load i128, ptr %%pb, align 16
  %%ud = udiv i128 %%a, %%b
  %%ur = urem i128 %%a, %%b
  %%sd = sdiv i128 %%a, %%b
  %%sr = srem i128 %%a, %%b
  %%o = mul i64 %%iv, 64
  %%p0 = getelementptr inbounds i8, ptr @out, i64 %%o
  %%p1 = getelementptr inbounds i8, ptr %%p0, i64 16
  %%p2 = getelementptr inbounds i8, ptr %%p0, i64 32
  %%p3 = getelementptr inbounds i8, ptr %%p0, i64 48
  store i128 %%ud, ptr %%p0, align 16
  store i128 %%ur, ptr %%p1, align 16
  store i128 %%sd, ptr %%p2, align 16
  store i128 %%sr, ptr %%p3, align 16
  %%next = add i64 %%iv, 1
  store i64 %%next, ptr %%i, align 8
  %%more = icmp ult i64 %%next, %d
  br i1 %%more, label %%loop, label %%exit
exit:
  call void @h_write(ptr @out, i64 %d)
  call void @h_exit(i32 0)
  unreachable
}
`, len(ops), llBytes(ops), 64*n, n, 64*n)
}

// TestTiAgainstGo: LLVM's i128 division instructions, through the
// support pack's routines, give math/big's truncated quotient and
// remainder modulo 2^128 for every pair of tiPairs, MIN / -1 (MIN) and
// MIN % -1 (0) included, on each run target.
func TestTiAgainstGo(t *testing.T) {
	ps := tiPairs()
	var ops, want []byte
	for _, p := range ps {
		ops = append(ops, p.bytes()...)
		ua, ub := tiBig(p.a, false), tiBig(p.b, false)
		sa, sb := tiBig(p.a, true), tiBig(p.b, true)
		want = append(want, tiBytes(new(big.Int).Quo(ua, ub))...)
		want = append(want, tiBytes(new(big.Int).Rem(ua, ub))...)
		want = append(want, tiBytes(new(big.Int).Quo(sa, sb))...)
		want = append(want, tiBytes(new(big.Int).Rem(sa, sb))...)
	}
	h := tiHarness(ops, len(ps))
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			exe := buildHarness(t, target, h, linkOpts{})
			out, code := runExe(t, target, exe, "")
			if code != 0 {
				t.Fatalf("exit status %#x", uint32(code))
			}
			got := []byte(out)
			if len(got) != len(want) {
				t.Fatalf("output is %d bytes, want %d", len(got), len(want))
			}
			names := []string{"udiv", "urem", "sdiv", "srem"}
			bad := 0
			for i, p := range ps {
				for k, name := range names {
					off := 64*i + 16*k
					if g, w := got[off:off+16], want[off:off+16]; string(g) != string(w) {
						if bad++; bad <= 10 {
							t.Errorf("%s(%#x:%016x, %#x:%016x) = %x, want %x (little-endian)", name,
								p.a[1], p.a[0], p.b[1], p.b[0], g, w)
						}
					}
				}
			}
			if bad > 0 {
				t.Fatalf("%d of %d results differ from math/big", bad, 4*len(ps))
			}
			t.Logf("%d pairs, %d results equal to math/big", len(ps), 4*len(ps))
		})
	}
}

// TestTiDivideByZeroTraps: each routine traps on a zero divisor
// (llvm.trap, ud2): STATUS_ILLEGAL_INSTRUCTION on Windows, SIGILL on Linux.
func TestTiDivideByZeroTraps(t *testing.T) {
	for _, op := range []string{"udiv", "urem", "sdiv", "srem"} {
		h := fmt.Sprintf(`
@z = internal global i128 0, align 16
@x = internal global i128 12345, align 16

define void @"mk.std.task.Start"(ptr %%sp) noreturn nounwind {
  %%a = load volatile i128, ptr @x, align 16
  %%b = load volatile i128, ptr @z, align 16
  %%r = %s i128 %%a, %%b
  store volatile i128 %%r, ptr @x, align 16
  call void @h_exit(i32 0)
  unreachable
}
`, op)
		for _, target := range runTargets {
			t.Run(op+"/"+target.Name, func(t *testing.T) {
				exe := buildHarness(t, target, h, linkOpts{})
				_, code := runExe(t, target, exe, "")
				want := 132 // 128 + SIGILL
				if target.OS == types.OSWindows {
					want = int(int32(-0x3fffffe3)) // 0xC000001D
				}
				if code != want && uint32(code) != uint32(want) {
					t.Fatalf("exit status %#x, want %#x", uint32(code), uint32(want))
				}
			})
		}
	}
}

// tiCall matches an instruction that calls or tail-calls a 128-bit
// division routine (Mach-O spells them with one more underscore).
var tiCall = regexp.MustCompile(`(?m)^\s*(call|callq|jmp|jmpq|bl|b)\s+(_*|_?mk\.support\.)(udiv|umod|div|mod)ti3\b|__udivmodti4|__divmodti4`)

// TestTiNoSelfCall compiles the support pack of every target at -O2
// and -O3 (the driver's level, compileOpts) to assembly: it defines the
// four routines and no instruction calls one, which would be a routine
// calling itself or another of them (an i128 division in their bodies).
// aarch64 has no run target, so this is its whole cover; x86_64-nutshell
// and x86_64-freestanding (general-regs-only, as its bins are) too.
func TestTiNoSelfCall(t *testing.T) {
	bin := needLLVM(t)
	cfgs := []Config{{Target: types.X86_64Nutshell}, {Target: types.X86_64Freestanding, GeneralRegsOnly: true}}
	for _, target := range types.Targets() {
		cfgs = append(cfgs, Config{Target: target})
	}
	for _, cfg := range cfgs {
		target := cfg.Target
		for _, opt := range []string{"-O2", "-O3"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				dir := t.TempDir()
				src, asm := filepath.Join(dir, "support.ll"), filepath.Join(dir, "support.s")
				var ll strings.Builder
				if err := EmitSupport(&Unit{}, nil, cfg, &ll); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(src, []byte(ll.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				runTool(t, tool(bin, "clang"), "--target="+target.LLVMTriple, "-S", opt, "-Wno-override-module",
					"-fverify-intermediate-code", "-o", asm, src)
				b, err := os.ReadFile(asm)
				if err != nil {
					t.Fatal(err)
				}
				s := string(b)
				if loc := tiCall.FindString(s); loc != "" {
					t.Fatalf("the support pack calls a 128-bit routine: %q", strings.TrimSpace(loc))
				}
				// Each body is defined, and the public name is a weak alias of
				// it, so a C source or archive that defines the name wins; on
				// Mach-O the routine is an external weak definition of the
				// public name itself.
				for _, sym := range []string{symUdivti3, symUmodti3, symDivti3, symModti3} {
					if target.OS == types.OSMacos {
						q := regexp.QuoteMeta("_" + sym)
						for _, want := range []string{`\n` + q + `:`, `\t\.globl\t` + q + `\s`, `\t\.weak_definition\t` + q + `\s`} {
							if !regexp.MustCompile(want).MatchString(s) {
								t.Errorf("want /%s/ for %s", want, sym)
							}
						}
						continue
					}
					body, alias, weak := tiBody(sym)+":", sym+" = "+tiBody(sym), "\t.weak\t"+sym+"\n"
					if !strings.Contains(s, "\n"+body) || !strings.Contains(s, "\n"+alias+"\n") || !strings.Contains(s, weak) {
						t.Errorf("want the body %q, %q and the alias %q for %s", body, strings.TrimSpace(weak), alias, sym)
					}
				}
			})
		}
	}
}

// TestTiRowsDiscardable: each routine is an internal body with a
// weak alias of the public name, and the body has its own row, the
// discardable kind funcRow gives a Mk function (its comdat's associative
// section on COFF, !associated on ELF); none is in the support pack's own
// row array, which FuncTable references; so a release link of a program
// that divides no i128 keeps none of them (the release hello's bytes are
// unchanged).
func TestTiRowsDiscardable(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			var sb strings.Builder
			if err := EmitSupport(funcTableUnit(e), e, Config{Target: target, Profile: types.Release}, &sb); err != nil {
				t.Fatal(err)
			}
			ll := sb.String()
			own := regexp.MustCompile(`(?m)^@mk\.support\.funcs = .*$`).FindString(ll)
			if own == "" || !strings.Contains(own, "ptr @memcpy") {
				t.Fatalf("no support row array with memcpy's row: %q", own)
			}
			for _, sym := range []string{symUdivti3, symUmodti3, symDivti3, symModti3} {
				q, qb := regexp.QuoteMeta(sym), regexp.QuoteMeta(tiBody(sym))
				if !regexp.MustCompile(`(?m)^define internal .*@` + qb + `\(`).MatchString(ll) {
					t.Errorf("%s's body %s is not an internal function", sym, tiBody(sym))
				}
				if !regexp.MustCompile(`(?m)^@` + q + ` = weak alias .*, ptr @` + qb + `$`).MatchString(ll) {
					t.Errorf("%s is not a weak alias of %s", sym, tiBody(sym))
				}
				row := `(?m)^@mk\.fr\.` + qb + ` = private constant \{ ptr, ptr, i32 \} \{ ptr @` + qb + `, ptr null, i32 2 \}`
				if target.OS == types.OSWindows {
					row += `, section "\.mkfn\$M", comdat\(\$` + qb + `\), align 8$`
					if !strings.Contains(ll, "$"+tiBody(sym)+" = comdat nodeduplicate") ||
						!regexp.MustCompile(`(?m)^define .*@`+qb+`\(.*\) .*comdat\(\$`+qb+`\)`).MatchString(ll) {
						t.Errorf("%s is not in its comdat", tiBody(sym))
					}
				} else {
					row += `, section "mk_funcs", align 8, !associated (![0-9]+)$`
				}
				if !regexp.MustCompile(row).MatchString(ll) {
					t.Errorf("no discardable row for %s (want /%s/)", tiBody(sym), row)
				}
				if strings.Contains(own, "@"+sym) || strings.Contains(own, "@"+tiBody(sym)) {
					t.Errorf("%s has a row in the support pack's own array, which keeps it", sym)
				}
				if regexp.MustCompile(`(?m)^@llvm\.used = .*ptr @(` + q + `|` + qb + `)\b`).MatchString(ll) {
					t.Errorf("%s is in llvm.used", sym)
				}
			}
			clangC(t, target, t.TempDir(), "support", ll, "-O2")
		})
	}
}
