// Why: runtime checks (overflow, division, shifts, casts) and the panic
// report must trap with the right code and site in a real program.
// What: TestPanicReport (a trap prints `panic: <code> at
// <file>:<line>:<col>` and exits 101, with a stub PanicAt before std/rt
// runs), TestOverflow, DivideChecks, ShiftChecks, CastChecks and the
// signed enum tag tests.
// How: hand-built check functions in dev (-O0) and release (-O2) objects are
// called by a harness per case, on Windows and WSL Linux.

package codegen

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// harnessRT is the stub runtime: h_num writes a decimal, PanicAt writes the
// report and exits 101.
const harnessRT = `
define internal void @h_num(i64 %v) nounwind {
entry:
  %buf = alloca [24 x i8], align 1
  %i = alloca i64, align 8
  %x = alloca i64, align 8
  store i64 24, ptr %i, align 8
  %neg = icmp slt i64 %v, 0
  %a = sub i64 0, %v
  %abs = select i1 %neg, i64 %a, i64 %v
  store i64 %abs, ptr %x, align 8
  br label %loop
loop:
  %iv = load i64, ptr %i, align 8
  %xv = load i64, ptr %x, align 8
  %d = urem i64 %xv, 10
  %q = udiv i64 %xv, 10
  %c = add i64 %d, 48
  %c8 = trunc i64 %c to i8
  %i1 = sub i64 %iv, 1
  %p = getelementptr inbounds i8, ptr %buf, i64 %i1
  store i8 %c8, ptr %p, align 1
  store i64 %i1, ptr %i, align 8
  store i64 %q, ptr %x, align 8
  %more = icmp ne i64 %q, 0
  br i1 %more, label %loop, label %sign
sign:
  br i1 %neg, label %minus, label %out
minus:
  %j = load i64, ptr %i, align 8
  %j1 = sub i64 %j, 1
  %pm = getelementptr inbounds i8, ptr %buf, i64 %j1
  store i8 45, ptr %pm, align 1
  store i64 %j1, ptr %i, align 8
  br label %out
out:
  %k = load i64, ptr %i, align 8
  %pk = getelementptr inbounds i8, ptr %buf, i64 %k
  %n = sub i64 24, %k
  call void @h_write(ptr %pk, i64 %n)
  ret void
}

@s.panic = private constant [7 x i8] c"panic: "
@s.at = private constant [4 x i8] c" at "
@s.colon = private constant [1 x i8] c":"
@s.nl = private constant [1 x i8] c"\0A"

define void @"mk.std.rt.PanicAt"(i32 %code, ptr %site) noreturn nounwind {
  call void @h_write(ptr @s.panic, i64 7)
  %c = zext i32 %code to i64
  call void @h_num(i64 %c)
  call void @h_write(ptr @s.at, i64 4)
  %f = load ptr, ptr %site, align 8
  %lp = getelementptr inbounds i8, ptr %site, i64 8
  %fl = load i64, ptr %lp, align 8
  call void @h_write(ptr %f, i64 %fl)
  call void @h_write(ptr @s.colon, i64 1)
  %linep = getelementptr inbounds i8, ptr %site, i64 16
  %line = load i32, ptr %linep, align 4
  %line64 = zext i32 %line to i64
  call void @h_num(i64 %line64)
  call void @h_write(ptr @s.colon, i64 1)
  %colp = getelementptr inbounds i8, ptr %site, i64 20
  %col = load i32, ptr %colp, align 4
  %col64 = zext i32 %col to i64
  call void @h_num(i64 %col64)
  call void @h_write(ptr @s.nl, i64 1)
  call void @h_exit(i32 101)
  unreachable
}
`

// checkBodies are the functions the native check tests call.
func checkBodies(e *testEnv) []*mir.Body {
	cp := mir.Copy(place(2))
	// enum SE: i8 { A = -1, B = 0 }.
	se := e.enumT("SE", types.I8, []uint64{0xFF, 0})
	return []*mir.Body{
		binFn(e, "AddI32", mir.Add, types.I32, types.I32, cp),
		binFn(e, "SubU8", mir.Sub, types.U8, types.U8, cp),
		binFn(e, "MulInt", mir.Mul, types.Int, types.Int, cp),
		binFn(e, "DivI64", mir.Div, types.I64, types.I64, cp),
		binFn(e, "RemI32", mir.Rem, types.I32, types.I32, cp),
		binFn(e, "ShlU32", mir.Shl, types.U32, types.U32, cp),
		binFn(e, "ShrI16", mir.Shr, types.I16, types.I16, cp),
		binFn(e, "StrLt", mir.Lt, types.Str, types.Bool, cp),
		castFn(e, "F64ToI32", types.F64, types.I32, mir.CastChecked),
		castFn(e, "F64ToU32", types.F64, types.U32, mir.CastChecked),
		castFn(e, "F64ToF32", types.F64, types.F32, mir.CastChecked),
		castFn(e, "U32ToChar", types.U32, types.Char, mir.CastChecked),
		castFn(e, "IntToAB", types.Int, e.enumT("AB", types.U8, []uint64{0, 1}), mir.CastChecked),
		castFn(e, "I32ToU8", types.I32, types.U8, mir.CastChecked),
		castFn(e, "SEToInt", se, types.Int, mir.CastLossless),
		castFn(e, "IntToSE", types.Int, se, mir.CastChecked),
		castFn(e, "I32ToSE", types.I32, se, mir.CastChecked),
	}
}

// checkBuild compiles the support pack and the check pack once per target
// and profile; each case links its own harness.
type checkBuild struct {
	target types.Target
	dir    string
	objs   []string
}

// checkBuilds holds one checkBuild per target and opt, shared by the four
// check tests (TestOverflow, TestDivideChecks, TestShiftChecks, TestCastChecks) and TestPanicReport; its directories live under
// sharedDir, which TestMain removes.
var (
	checkMu     sync.Mutex
	checkBuilds = map[string]*checkBuild{}
)

func newCheckBuild(t *testing.T, target types.Target, opt string) *checkBuild {
	t.Helper()
	checkMu.Lock()
	defer checkMu.Unlock()
	key := target.Name + opt
	if cb := checkBuilds[key]; cb != nil {
		return cb
	}
	e := newTestEnv()
	ll := emit(t, e, unitOf(e, checkBodies(e)), target, types.Dev)
	dir := sharedTempDir(t, "check-"+key)
	cb := &checkBuild{target: target, dir: dir, objs: []string{
		clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
		clangC(t, target, dir, "pack", ll, opt),
	}}
	checkBuilds[key] = cb
	return cb
}

// run links a harness whose Start body is body and runs it.
func (c *checkBuild) run(t *testing.T, name, body string) (string, int) {
	t.Helper()
	osIR, kernel := harnessOS(c.target)
	decls := harnessDecls(c.target)
	h := osIR + harnessRT + decls + `
define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + body + `
  call void @h_exit(i32 0)
  unreachable
}
`
	dir := c.dir + "/" + name
	if err := mkdir(dir); err != nil {
		t.Fatal(err)
	}
	obj := clangC(t, c.target, dir, "harness", h, "-O0")
	exe := linkExe(t, c.target, dir, append(append([]string(nil), c.objs...), obj), linkOpts{kernel: kernel})
	return runExe(t, c.target, exe, "")
}

// checkDecls are the harness's declarations of the check functions, with
// #s and #u where C puts a signed or unsigned narrow integer's extension
// attribute (harnessDecls).
const checkDecls = `
declare i32 @mk.main.AddI32(i32, i32)
declare #u i8 @mk.main.SubU8(i8 #u, i8 #u)
declare i64 @mk.main.MulInt(i64, i64)
declare i64 @mk.main.DivI64(i64, i64)
declare i32 @mk.main.RemI32(i32, i32)
declare i32 @mk.main.ShlU32(i32, i32)
declare #s i16 @mk.main.ShrI16(i16 #s, i16 #s)
declare i32 @mk.main.F64ToI32(double)
declare i32 @mk.main.F64ToU32(double)
declare float @mk.main.F64ToF32(double)
declare i32 @mk.main.U32ToChar(i32)
declare #u i8 @mk.main.IntToAB(i64)
declare #u i8 @mk.main.I32ToU8(i32)
declare i64 @mk.main.SEToInt(i8 #s)
declare #s i8 @mk.main.IntToSE(i64)
declare #s i8 @mk.main.I32ToSE(i32)
`

// cExtends reports whether target's C ABI extends an i8 or i16 argument
// or result to 32 bits by its signedness, as clang applies it: x86_64
// System V and Darwin do, Win64 and AAPCS64 Linux do not.
func cExtends(target types.Target) bool {
	return target.Arch == types.ArchX64 && target.OS != types.OSWindows || target.OS == types.OSMacos
}

// harnessDecls are checkDecls for target: a caller that leaves the
// extension out passes an i8 -1 with whatever upper bits it has, and an O2
// callee that trusts signext reads it as 255 (seen on x86_64-linux).
func harnessDecls(target types.Target) string {
	s, u := "signext", "zeroext"
	if !cExtends(target) {
		s, u = "", ""
	}
	return strings.NewReplacer(" #s", " "+s, " #u", " "+u, "#s ", s+" ", "#u ", u+" ").Replace(checkDecls)
}

// callCheck is IR that calls the check function fn with args ("i8 -1,
// i32 2") as harnessDecls(target) declares it, its result into %res, the
// extension attributes at the call site too, as clang writes them.
func callCheck(target types.Target, res, fn, args string) string {
	sig, ok := mainSigs(harnessDecls(target))[fn]
	if !ok {
		panic("callCheck: no harness declaration of " + fn)
	}
	var as []string
	if strings.TrimSpace(args) != "" {
		for i, a := range strings.Split(args, ",") {
			ty, val, _ := strings.Cut(strings.TrimSpace(a), " ")
			if i >= len(sig.params) || strings.Fields(sig.params[i])[0] != ty {
				panic("callCheck: " + fn + " argument " + a + " against " + sig.String())
			}
			as = append(as, sig.params[i]+" "+val)
		}
	}
	ret := sig.ret
	if r, ext, ok := strings.Cut(ret, " "); ok {
		ret = ext + " " + r
	}
	return fmt.Sprintf("  %s = call %s @mk.main.%s(%s)\n", res, ret, fn, strings.Join(as, ", "))
}

// printCall is IR that calls fn with args, extends the result to i64 and
// prints it with a newline.
func printCall(target types.Target, n int, ret, fn, args string, signed bool) string {
	ext := "sext"
	if !signed {
		ext = "zext"
	}
	conv := fmt.Sprintf("  %%w%d = %s %s %%r%d to i64\n", n, ext, ret, n)
	if ret == "i64" {
		conv = fmt.Sprintf("  %%w%d = add i64 %%r%d, 0\n", n, n)
	}
	if ret == "float" {
		conv = fmt.Sprintf("  %%f%d = bitcast float %%r%d to i32\n  %%w%d = zext i32 %%f%d to i64\n", n, n, n, n)
	}
	return callCheck(target, fmt.Sprintf("%%r%d", n), fn, args) + conv +
		fmt.Sprintf("  call void @h_num(i64 %%w%d)\n  call void @h_write(ptr @s.nl, i64 1)\n", n)
}

func f64(x float64) string { return fmt.Sprintf("0x%016X", math.Float64bits(x)) }

// TestOverflow: checked + - * of i32, u8 and int.
func TestOverflow(t *testing.T) { s_g158(t, "overflow") }

// TestDivideChecks: / and % by zero and MIN / -1.
func TestDivideChecks(t *testing.T) { s_g158(t, "divide") }

// TestShiftChecks: shift amounts at and past the width, and negative.
func TestShiftChecks(t *testing.T) { s_g158(t, "shift") }

// TestCastChecks: CastChecked NaN and bounds plus these cases
// (f64 to f32 overflow, surrogate and out-of-range char, enum tag 2 of
// {A, B}, -0.5 to u32 is 0, -2147483648.5 to i32 truncates without a panic,
// 2147483648.0 to i32 panics).
func TestCastChecks(t *testing.T) { s_g158(t, "cast") }

// TestSignedEnumTag: a payload-free enum with a signed tag type,
// enum SE: i8 { A = -1, B = 0 }. int(SE.A) is -1 (sext, not zext);
// Cast[SE](-1) is SE.A; Cast[SE] from an i32 200, an int 255 (outside i8)
// and an int -2 (an i8 that is no discriminant) panic with PanicCast.
func TestSignedEnumTag(t *testing.T) { s_g158(t, "signedenum") }

// TestHarnessExt: the native check harness calls the check pack
// as C would, so on every target each declaration it writes carries the
// extension attributes, on the result and on each parameter, that the
// pack's definition carries. The harness side is written from the C ABI
// rule (cExtends), not from codegen, so the two are independent; the clang
// goldens of internal/abi pin the rule itself (p_i8, p_Ei8: i8 noundef
// signext on x86_64-linux). An O0 caller that leaves an i8 -1 unextended
// against an O2 callee that trusts signext reads 255, which is how
// TestSignedEnumTag failed on x86_64-linux at O2 only.
func TestHarnessExt(t *testing.T) {
	for _, target := range types.Targets() {
		e := newTestEnv()
		ll := emit(t, e, unitOf(e, checkBodies(e)), target, types.Dev)
		defs := mainSigs(ll)
		decls := mainSigs(harnessDecls(target))
		if len(decls) == 0 {
			t.Fatalf("%s: no harness declarations parsed", target.Name)
		}
		for name, d := range decls {
			def, ok := defs[name]
			if !ok {
				t.Errorf("%s: the pack defines no %s", target.Name, name)
				continue
			}
			if d.String() != def.String() {
				t.Errorf("%s: %s: harness declares %s, pack defines %s", target.Name, name, d, def)
			}
		}
	}
}

// irSig is the types and extension attributes of an IR function signature.
type irSig struct {
	ret    string
	params []string
}

func (s irSig) String() string { return s.ret + " (" + strings.Join(s.params, ", ") + ")" }

// typeExt is "type" or "type signext" / "type zeroext" for one IR type and
// its attribute words.
func typeExt(words []string) string {
	ty, ext := "", ""
	for _, w := range words {
		switch {
		case w == "signext" || w == "zeroext":
			ext = " " + w
		case ty == "" && (strings.HasPrefix(w, "i") || w == "ptr" || w == "float" || w == "double" || w == "void"):
			ty = w
		}
	}
	return ty + ext
}

var sigLine = regexp.MustCompile(`(?m)^(?:declare|define)\s+(.*?)@mk\.main\.(\w+)\((.*?)\)`)

// mainSigs maps each mk.main function declared or defined in ll to its
// signature's types and extension attributes.
func mainSigs(ll string) map[string]irSig {
	out := map[string]irSig{}
	for _, m := range sigLine.FindAllStringSubmatch(ll, -1) {
		pre := strings.Fields(m[1])
		// The result type is the last word before the name; its attributes
		// come before it.
		s := irSig{ret: typeExt(append([]string{pre[len(pre)-1]}, pre[:len(pre)-1]...))}
		if strings.TrimSpace(m[3]) != "" {
			for _, p := range strings.Split(m[3], ",") {
				s.params = append(s.params, typeExt(strings.Fields(p)))
			}
		}
		out[m[2]] = s
	}
	return out
}

// checkGroup is the check test a check function belongs to.
func checkGroup(fn string) string {
	switch {
	case strings.HasPrefix(fn, "SETo"), strings.HasSuffix(fn, "ToSE"):
		return "signedenum"
	case strings.HasPrefix(fn, "Add"), strings.HasPrefix(fn, "Sub"), strings.HasPrefix(fn, "Mul"):
		return "overflow"
	case strings.HasPrefix(fn, "Div"), strings.HasPrefix(fn, "Rem"):
		return "divide"
	case strings.HasPrefix(fn, "Sh"):
		return "shift"
	}
	return "cast"
}

// s_g158 runs group's values that must not panic in one executable, then
// one executable per panicking check, each reporting its code and site.
func s_g158(t *testing.T, group string) {
	type call struct {
		ret, fn, args string
		signed        bool
		want          string
	}
	ok := []call{
		{"i32", "AddI32", "i32 2147483646, i32 1", true, "2147483647"},
		{"i8", "SubU8", "i8 5, i8 3", false, "2"},
		{"i64", "DivI64", "i64 -7, i64 2", true, "-3"},
		{"i32", "RemI32", "i32 -7, i32 2", true, "-1"},
		{"i32", "ShlU32", "i32 1, i32 31", false, "2147483648"},
		{"i16", "ShrI16", "i16 -8, i16 1", true, "-4"},
		{"i32", "F64ToU32", "double " + f64(-0.5), false, "0"},
		{"i32", "F64ToI32", "double " + f64(-2147483648.5), true, "-2147483648"},
		{"i32", "F64ToI32", "double " + f64(2147483647.9), true, "2147483647"},
		{"float", "F64ToF32", "double " + f64(0.1), false, fmt.Sprint(math.Float32bits(0.1))},
		{"float", "F64ToF32", "double " + f64(math.Inf(-1)), false, fmt.Sprint(math.Float32bits(float32(math.Inf(-1))))},
		{"i32", "U32ToChar", "i32 1114111", false, "1114111"},
		{"i8", "IntToAB", "i64 1", false, "1"},
		{"i8", "I32ToU8", "i32 255", false, "255"},
		{"i64", "SEToInt", "i8 -1", true, "-1"},
		{"i64", "SEToInt", "i8 0", true, "0"},
		{"i8", "IntToSE", "i64 -1", true, "-1"},
		{"i8", "IntToSE", "i64 0", true, "0"},
		{"i8", "I32ToSE", "i32 -1", true, "-1"},
	}
	type trap struct {
		name, fn, ret, args string
		code                types.PanicCode
		line, col           int
	}
	traps := []trap{
		{"overflow", "AddI32", "i32", "i32 2147483647, i32 1", types.PanicOverflow, 2, 5},
		{"underflow", "SubU8", "i8", "i8 3, i8 5", types.PanicOverflow, 2, 5},
		{"mul", "MulInt", "i64", "i64 4611686018427387904, i64 2", types.PanicOverflow, 2, 5},
		{"divzero", "DivI64", "i64", "i64 1, i64 0", types.PanicDivideByZero, 2, 5},
		{"minover", "DivI64", "i64", "i64 -9223372036854775808, i64 -1", types.PanicDivideByZero, 2, 5},
		{"shift", "ShlU32", "i32", "i32 1, i32 32", types.PanicShiftAmount, 2, 5},
		{"shiftneg", "ShrI16", "i16", "i16 1, i16 -1", types.PanicShiftAmount, 2, 5},
		{"f32over", "F64ToF32", "float", "double " + f64(1e300), types.PanicCast, 4, 9},
		{"i32over", "F64ToI32", "i32", "double " + f64(2147483648.0), types.PanicCast, 4, 9},
		{"nan", "F64ToI32", "i32", "double " + f64(math.NaN()), types.PanicCast, 4, 9},
		{"surrogate", "U32ToChar", "i32", "i32 55296", types.PanicCast, 4, 9},
		{"charover", "U32ToChar", "i32", "i32 1114112", types.PanicCast, 4, 9},
		{"enum", "IntToAB", "i8", "i64 2", types.PanicCast, 4, 9},
		{"u8over", "I32ToU8", "i8", "i32 256", types.PanicCast, 4, 9},
		{"u8neg", "I32ToU8", "i8", "i32 -1", types.PanicCast, 4, 9},
		{"se200", "I32ToSE", "i8", "i32 200", types.PanicCast, 4, 9},
		{"se255", "IntToSE", "i8", "i64 255", types.PanicCast, 4, 9},
		{"seneg2", "IntToSE", "i8", "i64 -2", types.PanicCast, 4, 9},
	}
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				cb := newCheckBuild(t, target, opt)
				var body strings.Builder
				var want strings.Builder
				for i, c := range ok {
					if checkGroup(c.fn) != group {
						continue
					}
					body.WriteString(printCall(target, i, c.ret, c.fn, c.args, c.signed))
					want.WriteString(c.want + "\n")
				}
				out, code := cb.run(t, "ok-"+group, body.String())
				if code != 0 || out != want.String() {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want.String())
				}
				for _, tr := range traps {
					if checkGroup(tr.fn) != group {
						continue
					}
					out, code := cb.run(t, tr.name, callCheck(target, "%r", tr.fn, tr.args))
					w := fmt.Sprintf("panic: %d at main/t.mk:%d:%d\n", tr.code, tr.line, tr.col)
					if code != 101 || out != w {
						t.Errorf("%s: exit %d, output %q; want 101 and %q", tr.name, code, out, w)
					}
				}
			})
		}
	}
}

// TestPanicReport: an overflow prints its report with the site of the
// statement and exits 101.
func TestPanicReport(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			if _, why := hostRunner(target); why != "" {
				skipRunner(t, why)
			}
			cb := newCheckBuild(t, target, "-O0")
			out, code := cb.run(t, "p", callCheck(target, "%r", "AddI32", "i32 2147483647, i32 1"))
			if code != 101 || out != "panic: 1 at main/t.mk:2:5\n" {
				t.Fatalf("exit %d, output %q", code, out)
			}
		})
	}
}
