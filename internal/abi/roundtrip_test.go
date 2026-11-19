// Why: agreeing with clang's text is not proof; only a real call across two
// objects shows that every byte of every argument and result arrives.
// What: TestABIHostRoundTrip, cTableExt and the garbage rows (garbageRows).
// How: an IR caller and an IR callee, both lowered by Lower, are compiled by
// clang as two separate objects (so nothing is inlined across the call),
// linked CRT-free with a small support module and run on each x86_64 host
// (Windows natively, Linux natively or through wsl.exe). Every byte of every
// field of every parameter and result is compared. The garbage rows
// f(a: i8, b: u16), g(c: bool) and h() u8 pair an abi-lowered side with a
// C-like side written from C's extension rules (not from abi) that leaves
// garbage in the upper bits wherever C allows it, so a callee or caller that
// trusts a wrong extension reads a wrong value. The test runs clang, ld.lld,
// lld-link and llvm-lib as processes, on LLVM IR only (no C). It does not go
// through internal/toolchain, which abi may not import.

package abi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// rtHost is one x86_64 target the round trip builds and runs.
type rtHost struct {
	target types.Target
	triple string
	layout string // the datalayout clang 21.1.8 and 18.1.3 report
}

var rtHosts = []rtHost{
	{types.X86_64Windows, "x86_64-pc-windows-msvc",
		"e-m:w-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"},
	{types.X86_64Linux, "x86_64-unknown-linux-gnu",
		"e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"},
}

// llvmBin finds the directory holding clang: MK_LLVM, the default install
// location, then PATH.
func llvmBin() (string, bool) {
	exe := func(n string) string {
		if runtime.GOOS == "windows" {
			return n + ".exe"
		}
		return n
	}
	var dirs []string
	if d := os.Getenv("MK_LLVM"); d != "" {
		dirs = append(dirs, d)
	}
	if runtime.GOOS == "windows" {
		dirs = append(dirs, `C:\Program Files\LLVM\bin`)
	} else {
		for v := 21; v >= 18; v-- {
			dirs = append(dirs, fmt.Sprintf("/usr/lib/llvm-%d/bin", v))
		}
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, exe("clang"))); err == nil {
			return d, true
		}
	}
	if p, err := exec.LookPath("clang"); err == nil {
		return filepath.Dir(p), true
	}
	return "", false
}

func tool(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

func runTool(t *testing.T, argv ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%q: %v\n%s", argv, err, out)
	}
}

// cTableExt is what C code on target guarantees about a scalar below 32 bits
// that it passes or returns: C's extension rules per target, written here
// independently of abi.
func cTableExt(target types.Target, ty types.TypeID) Ext {
	switch {
	case target.OS == types.OSWindows:
		if ty == types.Bool {
			return ZeroExt
		}
		return ExtNone
	case target.Arch == types.ArchX64 || target.OS == types.OSMacos:
		switch ty {
		case types.Bool, types.U8, types.U16:
			return ZeroExt
		case types.I8, types.I16:
			return SignExt
		}
	}
	return ExtNone
}

// TestABIHostRoundTrip builds and runs the round trip for each x86_64 target
// this host can run: every suite row that is not variadic and returns, plus
// the garbage rows.
func TestABIHostRoundTrip(t *testing.T) {
	bin, ok := llvmBin()
	if !ok {
		t.Skip("SKIP: no clang found (MK_LLVM, the default location, PATH); the gate run must not skip this")
	}
	w := loadSuite(t)
	for _, h := range rtHosts {
		t.Run(h.target.Name, func(t *testing.T) {
			runner, ok := rtRunner(h.target)
			b := newRTBuild(w, h.target)
			rows := b.rows()
			if len(rows) < 50 {
				t.Fatalf("only %d round-trip rows; 7.2 asks for 50", len(rows))
			}
			dir := t.TempDir()
			exe := b.build(t, bin, h, dir, rows)
			if !ok {
				t.Skipf("SKIP: built %s, but this host cannot run %s", exe, h.target.Name)
			}
			if len(runner) > 0 && !linuxRun() {
				t.Skipf("%s (built %s; its run starts wsl.exe)", linuxOff, exe)
			}
			out := rtRun(t, runner, exe)
			fails := 0
			for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
				if strings.HasPrefix(ln, "FAIL ") {
					fails++
					t.Errorf("%s: %s (suite rows: bit i is parameter i, bit 31 the result; cg23: bit i is got[i])", h.target.Name, ln)
				}
			}
			if !strings.Contains(out, "rt-done\n") {
				t.Fatalf("round trip did not finish; output %q", out)
			}
			t.Logf("%s: %d suite rows and 3 extension rows round-tripped through %s, %d failed", h.target.Name, len(rows), bin, fails)
		})
	}
}

// rtRunner is the command prefix that runs a target's executable here.
func rtRunner(target types.Target) ([]string, bool) {
	switch {
	case target.OS == types.OSWindows:
		return nil, runtime.GOOS == "windows" && runtime.GOARCH == "amd64"
	case runtime.GOOS == "linux":
		return nil, runtime.GOARCH == "amd64"
	case runtime.GOOS == "windows":
		if p, err := exec.LookPath("wsl.exe"); err == nil {
			return []string{p, "-e"}, true
		}
	}
	return nil, false
}

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set; the gate runs those leaves in their own pass, under
// the WSL lock.
const linuxOff = "MK_LINUX=1 not set"

// linuxRun reports whether this run may start wsl.exe, MK_LINUX=1.
func linuxRun() bool { return os.Getenv("MK_LINUX") == "1" }

// wslGuard panics when a wsl.exe is about to start without MK_LINUX=1: a
// leaf that reaches it missed its linuxRun check.
//
// With MK_LINUX=1 it also panics unless the run holds the WSL lock
// (tools/wsl-lock.ps1 sets MK_WSL_LOCKED=1), so a Linux run outside the
// lock cannot happen by accident; MK_WSL_UNLOCKED_OK=1 allows one on
// purpose.
func wslGuard() {
	if !linuxRun() {
		panic("wsl.exe started without MK_LINUX=1; the leaf must skip with " + linuxOff)
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" && os.Getenv("MK_WSL_UNLOCKED_OK") != "1" {
		panic("wsl.exe started with MK_LINUX=1 outside the WSL lock (MK_WSL_LOCKED is not 1): run under pwsh -NoProfile -File tools/wsl-lock.ps1, or set MK_WSL_UNLOCKED_OK=1")
	}
}

func rtRun(t *testing.T, prefix []string, exe string) string {
	t.Helper()
	path := exe
	if len(prefix) > 0 {
		wslGuard()
		// wsl.exe sees C:\x\y as /mnt/c/x/y.
		p := filepath.ToSlash(exe)
		if len(p) > 1 && p[1] == ':' {
			p = "/mnt/" + strings.ToLower(p[:1]) + p[2:]
		}
		path = p
	}
	argv := append(append([]string(nil), prefix...), path)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		t.Logf("stderr: %s", stderr.String())
	}
	if err != nil {
		t.Errorf("%q: %v (output %q)", argv, err, stdout.String())
	}
	return stdout.String()
}

// rtBuild generates the caller module (with the support code and all data)
// and the callee module for one target.
type rtBuild struct {
	w      *world
	target types.Target
	v      *view
	caller strings.Builder
	callee strings.Builder
	consts strings.Builder // caller-module globals
	ext    strings.Builder // callee-module declarations of those globals
	names  []string        // row names, in row order
}

func newRTBuild(w *world, target types.Target) *rtBuild {
	return &rtBuild{w: w, target: target, v: newView(w.tt, w.layout, target)}
}

// rows are the suite rows a round trip can express: not variadic (a callee
// would need va_arg), and returning (r_never cannot).
func (b *rtBuild) rows() []row {
	var out []row
	for _, r := range b.w.rows {
		if r.variadic || r.result == types.Never {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (b *rtBuild) build(t *testing.T, bin string, h rtHost, dir string, rows []row) string {
	t.Helper()
	for i, r := range rows {
		b.row(i, r, Lower(b.w.tt, b.w.layout, b.target, r.params, r.fixed, r.variadic, r.result))
	}
	b.garbageRows()
	hdr := fmt.Sprintf("target datalayout = %q\ntarget triple = %q\n\n", h.layout, h.triple)
	callerLL := hdr + b.support() + b.consts.String() + b.caller.String() + b.mainFn()
	calleeLL := hdr + b.ext.String() + "declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)\n\n" + b.callee.String() +
		"attributes #0 = { nounwind noinline }\n"
	files := map[string]string{"caller.ll": callerLL, "callee.ll": calleeLL}
	var objs []string
	for _, n := range []string{"caller.ll", "callee.ll"} {
		ll := filepath.Join(dir, n)
		if err := os.WriteFile(ll, []byte(files[n]), 0o644); err != nil {
			t.Fatal(err)
		}
		obj := strings.TrimSuffix(ll, ".ll") + ".o"
		runTool(t, tool(bin, "clang"), "--target="+h.triple, "-c", "-O2", "-Wno-override-module", "-o", obj, ll)
		objs = append(objs, obj)
	}
	if b.target.OS == types.OSWindows {
		def := filepath.Join(dir, "kernel32.def")
		if err := os.WriteFile(def, []byte("LIBRARY kernel32.dll\nEXPORTS\nExitProcess\nGetStdHandle\nWriteFile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lib := filepath.Join(dir, "kernel32.lib")
		runTool(t, tool(bin, "llvm-lib"), "/def:"+def, "/machine:x64", "/out:"+lib)
		exe := filepath.Join(dir, "rt.exe")
		runTool(t, append([]string{tool(bin, "lld-link"), "/entry:rt_start", "/subsystem:console", "/nodefaultlib",
			"/out:" + exe}, append(objs, lib)...)...)
		return exe
	}
	exe := filepath.Join(dir, "rt")
	runTool(t, append([]string{tool(bin, "ld.lld"), "-static", "-e", "_start", "-o", exe}, objs...)...)
	return exe
}

// value is one parameter or result's data: its pattern bytes, which bytes
// are data (not padding), and the buffer length (at least the bytes the
// register parts cover, so a part load never runs past it).
type value struct {
	size uint64
	buf  uint64
	pat  []byte
	mask []byte
}

func llBytes(b []byte) string {
	var s strings.Builder
	s.WriteString("c\"")
	for _, c := range b {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '_' {
			s.WriteByte(c)
		} else {
			fmt.Fprintf(&s, "\\%02X", c)
		}
	}
	s.WriteString("\"")
	return s.String()
}

// newValue fills a value of type ty with a pattern unique to (row, index):
// every data byte from the leaves, a bool leaf 0 or 1.
func (b *rtBuild) newValue(ty types.TypeID, s Slot, seed int) value {
	size, _ := b.v.sizeAlign(ty)
	n := max(size, 8*uint64(len(s.Parts)))
	val := value{size: size, buf: (n+7)/8*8 + 8}
	val.pat = make([]byte, val.buf)
	val.mask = make([]byte, val.buf)
	for i := range val.pat {
		val.pat[i] = byte(seed*37 + i*11 + 0x5B)
	}
	var f flat
	b.v.walk(&f, ty, 0)
	for _, l := range f.leaves {
		for j := l.off; j < l.off+l.size && j < size; j++ {
			val.mask[j] = 0xFF
		}
	}
	b.boolBytes(ty, 0, val.pat)
	return val
}

// boolBytes makes every bool leaf 0 or 1, so a bool passed as i1 keeps it.
func (b *rtBuild) boolBytes(ty types.TypeID, off uint64, pat []byte) {
	t := b.w.tt.Get(ty)
	switch t.Kind {
	case types.KBool:
		pat[off] &= 1
		return
	case types.KArray:
		es, _ := b.v.sizeAlign(t.Elem)
		if es == 0 {
			return
		}
		n := b.v.layoutOf(ty).Shape.Size / es
		for i := uint64(0); i < n; i++ {
			b.boolBytes(t.Elem, off+i*es, pat)
		}
		return
	}
	if _, ok := b.v.scalarOf(ty); ok {
		return
	}
	switch t.Kind {
	case types.KVoid, types.KNever, types.KStr, types.KSlice, types.KMutSlice:
		return
	}
	l := b.v.layoutOf(ty)
	for _, fl := range l.Fields {
		b.boolBytes(fl.Type, off+fl.Offset, pat)
	}
	for _, vr := range l.Variants {
		for _, fl := range vr.Fields {
			b.boolBytes(fl.Type, off+fl.Offset, pat)
		}
	}
}

// global defines a caller-module global and declares it in the callee.
func (b *rtBuild) global(name string, data []byte, constant bool) {
	kind := "global"
	if constant {
		kind = "constant"
	}
	fmt.Fprintf(&b.consts, "@%s = %s [%d x i8] %s, align 16\n", name, kind, len(data), llBytes(data))
	fmt.Fprintf(&b.ext, "@%s = external global [%d x i8], align 16\n", name, len(data))
}

// partType is the LLVM type of one register part.
func partType(p types.AbiPart) string {
	switch {
	case p.Class == types.RegVector:
		return "<2 x float>"
	case p.Class == types.RegFloat && p.Bytes == 4:
		return "float"
	case p.Class == types.RegFloat:
		return "double"
	}
	return fmt.Sprintf("i%d", 8*int(p.Bytes))
}

// slotScalar reports the LLVM scalar type of ty when it travels as one C
// scalar.
func (b *rtBuild) slotScalar(ty types.TypeID) (string, bool) {
	s, ok := b.v.scalarOf(ty)
	if !ok {
		return "", false
	}
	return scalarType(s), true
}

func attrs(s Slot) string {
	return " noundef" + strings.TrimRight(" "+extAttr(s.Ext), " ")
}

// fn writes into one module with fresh value names.
type fn struct {
	b *strings.Builder
	n int
}

func (f *fn) tmp() string { f.n++; return fmt.Sprintf("%%t%d", f.n) }

func (f *fn) line(format string, args ...any) { fmt.Fprintf(f.b, "  "+format+"\n", args...) }

// gep is the address of byte off of the global or pointer at.
func (f *fn) gep(at string, off uint64) string {
	if off == 0 {
		return at
	}
	r := f.tmp()
	f.line("%s = getelementptr inbounds i8, ptr %s, i64 %d", r, at, off)
	return r
}

// loadParts loads the values that carry slot s of type ty from the bytes at
// at: the scalar, or each register part at 8 times its index (x86_64).
func (b *rtBuild) loadParts(f *fn, ty types.TypeID, s Slot, at string) []string {
	if st, ok := b.slotScalar(ty); ok {
		if st == "i1" {
			r := f.tmp()
			f.line("%s = load i8, ptr %s, align 1", r, at)
			r2 := f.tmp()
			f.line("%s = trunc i8 %s to i1", r2, r)
			return []string{r2}
		}
		r := f.tmp()
		f.line("%s = load %s, ptr %s, align 1", r, st, at)
		return []string{r}
	}
	var out []string
	for j, p := range s.Parts {
		r := f.tmp()
		f.line("%s = load %s, ptr %s, align 1", r, partType(p), f.gep(at, 8*uint64(j)))
		out = append(out, r)
	}
	return out
}

// storeParts stores the values of slot s back to bytes at at.
func (b *rtBuild) storeParts(f *fn, ty types.TypeID, s Slot, vals []string, at string) {
	if st, ok := b.slotScalar(ty); ok {
		if st == "i1" {
			r := f.tmp()
			f.line("%s = zext i1 %s to i8", r, vals[0])
			f.line("store i8 %s, ptr %s, align 1", r, at)
			return
		}
		f.line("store %s %s, ptr %s, align 1", st, vals[0], at)
		return
	}
	for j, p := range s.Parts {
		f.line("store %s %s, ptr %s, align 1", partType(p), vals[j], f.gep(at, 8*uint64(j)))
	}
}

// partTypes is the LLVM types of slot s's values, as parameters.
func (b *rtBuild) valueTypes(ty types.TypeID, s Slot) []string {
	if st, ok := b.slotScalar(ty); ok {
		return []string{st}
	}
	var out []string
	for _, p := range s.Parts {
		out = append(out, partType(p))
	}
	return out
}

// retType is the LLVM return type of a Direct result.
func (b *rtBuild) retType(ty types.TypeID, s Slot) string {
	ts := b.valueTypes(ty, s)
	if len(ts) == 1 {
		return ts[0]
	}
	return "{ " + strings.Join(ts, ", ") + " }"
}

// row generates row k: its data, its callee @rt_k and its caller @rt_call_k,
// which returns a bit per differing parameter (bit 31 for the result).
func (b *rtBuild) row(k int, r row, sig Sig) {
	if b.target.OS == types.OSWindows {
		for _, s := range append([]Slot{sig.Ret}, sig.Params...) {
			if s.Kind == Direct && len(s.Parts) > 1 {
				panic(fmt.Sprintf("Win64 row %s: %d parts", r.name, len(s.Parts)))
			}
		}
	}
	name := fmt.Sprintf("rt_%d", k)
	b.names = append(b.names, r.name)
	vals := make([]value, len(r.params))
	for i, p := range r.params {
		vals[i] = b.newValue(p, sig.Params[i], k*16+i)
		b.global(fmt.Sprintf("%s_p%d_pat", name, i), vals[i].pat, true)
		b.global(fmt.Sprintf("%s_p%d_mask", name, i), vals[i].mask, true)
		b.global(fmt.Sprintf("%s_p%d_got", name, i), make([]byte, vals[i].buf), false)
	}
	var rv value
	hasRet := sig.Ret.Kind != Ignore
	if hasRet {
		rv = b.newValue(r.result, sig.Ret, k*16+15)
		b.global(name+"_r_pat", rv.pat, true)
		b.global(name+"_r_mask", rv.mask, true)
		b.global(name+"_r_got", make([]byte, rv.buf), false)
	}

	// The declaration both sides agree on.
	ret := "void"
	var retAttr string
	var params []string // types with attributes, without names
	if sig.Ret.Kind == Direct {
		ret = b.retType(r.result, sig.Ret)
		if _, ok := b.slotScalar(r.result); ok {
			retAttr = strings.TrimPrefix(attrs(sig.Ret), " ") + " "
		}
	}
	if sig.Ret.Kind == Indirect {
		params = append(params, fmt.Sprintf("ptr sret([%d x i8]) align %d", rv.size, sig.Ret.Align))
	}
	for i, p := range r.params {
		s := sig.Params[i]
		switch s.Kind {
		case Direct:
			_, scalar := b.slotScalar(p)
			for _, ty := range b.valueTypes(p, s) {
				if scalar {
					ty += attrs(s)
				}
				params = append(params, ty)
			}
		case Indirect:
			if s.Byval {
				params = append(params, fmt.Sprintf("ptr noundef byval([%d x i8]) align %d", vals[i].size, s.Align))
			} else {
				params = append(params, "ptr noundef")
			}
		}
	}

	// Callee.
	ce := &fn{b: &b.callee}
	var formals []string
	for j, p := range params {
		formals = append(formals, fmt.Sprintf("%s %%a%d", p, j))
	}
	fmt.Fprintf(&b.callee, "define dso_local %s%s @%s(%s) #0 {\nentry:\n", retAttr, ret, name, strings.Join(formals, ", "))
	j := 0
	if sig.Ret.Kind == Indirect {
		j++
	}
	for i, p := range r.params {
		s := sig.Params[i]
		got := fmt.Sprintf("@%s_p%d_got", name, i)
		switch s.Kind {
		case Direct:
			n := len(b.valueTypes(p, s))
			var args []string
			for x := 0; x < n; x++ {
				args = append(args, fmt.Sprintf("%%a%d", j+x))
			}
			b.storeParts(ce, p, s, args, got)
			j += n
		case Indirect:
			ce.line("call void @llvm.memcpy.p0.p0.i64(ptr align 16 %s, ptr align 1 %%a%d, i64 %d, i1 false)", got, j, vals[i].size)
			j++
		}
	}
	switch sig.Ret.Kind {
	case Ignore:
		ce.line("ret void")
	case Indirect:
		ce.line("call void @llvm.memcpy.p0.p0.i64(ptr align 1 %%a0, ptr align 16 @%s_r_pat, i64 %d, i1 false)", name, rv.size)
		ce.line("ret void")
	case Direct:
		parts := b.loadParts(ce, r.result, sig.Ret, "@"+name+"_r_pat")
		if len(parts) == 1 {
			ce.line("ret %s %s", ret, parts[0])
			break
		}
		agg := "undef"
		for x, pv := range parts {
			nv := ce.tmp()
			ce.line("%s = insertvalue %s %s, %s %s, %d", nv, ret, agg, b.valueTypes(r.result, sig.Ret)[x], pv, x)
			agg = nv
		}
		ce.line("ret %s %s", ret, agg)
	}
	b.callee.WriteString("}\n\n")
	fmt.Fprintf(&b.caller, "declare %s%s @%s(%s)\n\n", retAttr, ret, name, strings.Join(params, ", "))

	// Caller.
	cl := &fn{b: &b.caller}
	fmt.Fprintf(&b.caller, "define internal i32 @rt_call_%d() #1 {\nentry:\n", k)
	var actuals []string
	var sret string
	if sig.Ret.Kind == Indirect {
		sret = cl.tmp()
		cl.line("%s = alloca [%d x i8], align 16", sret, rv.buf)
		actuals = append(actuals, params[0]+" "+sret)
	}
	pi := len(actuals)
	for i, p := range r.params {
		s := sig.Params[i]
		pat := fmt.Sprintf("@%s_p%d_pat", name, i)
		switch s.Kind {
		case Direct:
			for _, a := range b.loadParts(cl, p, s, pat) {
				actuals = append(actuals, params[pi]+" "+a)
				pi++
			}
		case Indirect:
			arg := pat
			if !s.Byval {
				// Win64: a pointer to a copy the callee may change.
				arg = cl.tmp()
				cl.line("%s = alloca [%d x i8], align 16", arg, vals[i].buf)
				cl.line("call void @llvm.memcpy.p0.p0.i64(ptr align 16 %s, ptr align 16 %s, i64 %d, i1 false)", arg, pat, vals[i].size)
			}
			actuals = append(actuals, params[pi]+" "+arg)
			pi++
		}
	}
	call := fmt.Sprintf("call %s%s @%s(%s)", retAttr, ret, name, strings.Join(actuals, ", "))
	rgot := "@" + name + "_r_got"
	switch sig.Ret.Kind {
	case Direct:
		res := cl.tmp()
		cl.line("%s = %s", res, call)
		parts := []string{res}
		if ts := b.valueTypes(r.result, sig.Ret); len(ts) > 1 {
			parts = nil
			for x := range ts {
				e := cl.tmp()
				cl.line("%s = extractvalue %s %s, %d", e, ret, res, x)
				parts = append(parts, e)
			}
		}
		b.storeParts(cl, r.result, sig.Ret, parts, rgot)
	case Indirect:
		cl.line("%s", call)
		cl.line("call void @llvm.memcpy.p0.p0.i64(ptr align 16 %s, ptr align 16 %s, i64 %d, i1 false)", rgot, sret, rv.size)
	default:
		cl.line("%s", call)
	}
	acc := "0"
	cmp := func(pfx string, n uint64, bit int) {
		c := cl.tmp()
		cl.line("%s = call i32 @rt_cmp(ptr @%s_got, ptr @%s_pat, ptr @%s_mask, i64 %d)", c, pfx, pfx, pfx, n)
		sh := cl.tmp()
		cl.line("%s = shl i32 %s, %d", sh, c, bit)
		o := cl.tmp()
		cl.line("%s = or i32 %s, %s", o, acc, sh)
		acc = o
	}
	for i := range r.params {
		cmp(fmt.Sprintf("%s_p%d", name, i), vals[i].size, min(i, 30))
	}
	if hasRet {
		cmp(name+"_r", rv.size, 31)
	}
	cl.line("ret i32 %s", acc)
	b.caller.WriteString("}\n\n")
}

// garbageRows are the extension rows, in both directions. The C-like side
// follows cTableExt, never abi; garbage fills every bit above a value's
// width (the low byte's spare bits included) wherever C leaves it:
//
//   - got[0..2]: a C-like caller of f and g, through i32 declarations, calls
//     the abi-lowered callees, which must not trust an extension C does not
//     make;
//   - got[3]: a C-like h returns 0xC3 in an i32, garbage above unless C
//     extends a u8 result, to an abi-lowered caller;
//   - got[4..6]: abi-lowered callers pass truncated garbage registers to
//     C-like callees of f and g that trust the extensions C makes, so an abi
//     caller that omits one hands them garbage.
func (b *rtBuild) garbageRows() {
	k := len(b.names)
	b.names = append(b.names, "cg23 f(a: i8, b: u16), g(c: bool), h() u8")
	cfg := Lower(b.w.tt, b.w.layout, b.target, []types.TypeID{types.I8, types.U16}, 2, false, types.Void)
	cgg := Lower(b.w.tt, b.w.layout, b.target, []types.TypeID{types.Bool}, 1, false, types.Void)
	chg := Lower(b.w.tt, b.w.layout, b.target, nil, 0, false, types.U8)
	cAttr := func(ty types.TypeID) string { return attrs(Slot{Ext: cTableExt(b.target, ty)}) }
	retExt := strings.TrimPrefix(strings.TrimRight(extAttr(chg.Ret.Ext), " ")+" ", " ")
	const garbage = 0x6B6B6BFE
	arg := func(v uint32, bits uint, e Ext) int32 {
		m := uint32(1)<<bits - 1
		v &= m
		switch e {
		case ExtNone:
			return int32(v | garbage&^m)
		case SignExt:
			if v&(1<<(bits-1)) != 0 {
				return int32(v | ^m)
			}
		}
		return int32(v)
	}
	fmt.Fprintf(&b.consts, "@gx_got = global [8 x i32] zeroinitializer, align 16\n")
	fmt.Fprintf(&b.ext, "@gx_got = external global [8 x i32], align 16\n")
	hv := uint32(0xC3)
	if cTableExt(b.target, types.U8) == ExtNone {
		hv |= garbage &^ 0xFF
	}
	fmt.Fprintf(&b.consts, "@gx_hv = global i32 %d, align 4\n", int32(hv))
	fmt.Fprintf(&b.ext, "@gx_hv = external global i32, align 4\n")
	// The registers the abi-lowered callers truncate: garbage above -3,
	// 0xBEEF and false.
	fmt.Fprintf(&b.consts, "@gx_reg = global [3 x i32] [i32 %d, i32 %d, i32 %d], align 4\n",
		int32(garbage&^0xFF|0xFD), int32(garbage&^0xFFFF|0xBEEF), int32(garbage&^1))

	// Callees: gx_f and gx_g lowered by abi, gc_f and gc_g as C defines them
	// (trusting C's extensions), gx_h as C returns a u8. Each widens what it
	// received to i32 and stores it.
	store := func(dst *strings.Builder, name, attrA, attrB string, slot0 int) {
		fmt.Fprintf(dst, "define dso_local void @%s(i8%s %%a, i16%s %%b) #0 {\nentry:\n", name, attrA, attrB)
		fmt.Fprintf(dst, "  %%x = sext i8 %%a to i32\n  %%p = getelementptr inbounds i32, ptr @gx_got, i64 %d\n  store i32 %%x, ptr %%p, align 4\n", slot0)
		fmt.Fprintf(dst, "  %%y = zext i16 %%b to i32\n  %%q = getelementptr inbounds i32, ptr @gx_got, i64 %d\n  store i32 %%y, ptr %%q, align 4\n  ret void\n}\n\n", slot0+1)
	}
	storeBool := func(dst *strings.Builder, name, attr string, slot int) {
		fmt.Fprintf(dst, "define dso_local void @%s(i1%s %%c) #0 {\nentry:\n", name, attr)
		fmt.Fprintf(dst, "  %%x = zext i1 %%c to i32\n  %%p = getelementptr inbounds i32, ptr @gx_got, i64 %d\n  store i32 %%x, ptr %%p, align 4\n  ret void\n}\n\n", slot)
	}
	store(&b.callee, "gx_f", attrs(cfg.Params[0]), attrs(cfg.Params[1]), 0)
	storeBool(&b.callee, "gx_g", attrs(cgg.Params[0]), 2)
	store(&b.callee, "gc_f", cAttr(types.I8), cAttr(types.U16), 4)
	storeBool(&b.callee, "gc_g", cAttr(types.Bool), 6)
	fmt.Fprintf(&b.callee, "define dso_local i32 @gx_h() #0 {\nentry:\n  %%v = load volatile i32, ptr @gx_hv, align 4\n  ret i32 %%v\n}\n\n")

	// Callers.
	b.caller.WriteString("declare void @gx_f(i32, i32)\ndeclare void @gx_g(i32)\n")
	fmt.Fprintf(&b.caller, "declare %si8 @gx_h()\n", retExt)
	fmt.Fprintf(&b.caller, "declare void @gc_f(i8%s, i16%s)\ndeclare void @gc_g(i1%s)\n\n", attrs(cfg.Params[0]), attrs(cfg.Params[1]), attrs(cgg.Params[0]))
	fmt.Fprintf(&b.caller, "define internal i32 @rt_call_%d() #1 {\nentry:\n", k)
	fmt.Fprintf(&b.caller, "  call void @gx_f(i32 %d, i32 %d)\n", arg(0xFD, 8, cTableExt(b.target, types.I8)), arg(0xBEEF, 16, cTableExt(b.target, types.U16)))
	fmt.Fprintf(&b.caller, "  call void @gx_g(i32 %d)\n", arg(0, 1, cTableExt(b.target, types.Bool)))
	fmt.Fprintf(&b.caller, "  %%r = call %si8 @gx_h()\n", retExt)
	fmt.Fprintf(&b.caller, "  %%rz = zext i8 %%r to i32\n  %%p3 = getelementptr inbounds i32, ptr @gx_got, i64 3\n  store i32 %%rz, ptr %%p3, align 4\n")
	for i := range 3 {
		fmt.Fprintf(&b.caller, "  %%gp%d = getelementptr inbounds i32, ptr @gx_reg, i64 %d\n  %%g%d = load volatile i32, ptr %%gp%d, align 4\n", i, i, i, i)
	}
	fmt.Fprintf(&b.caller, "  %%ta = trunc i32 %%g0 to i8\n  %%tb = trunc i32 %%g1 to i16\n  %%tc = trunc i32 %%g2 to i1\n")
	fmt.Fprintf(&b.caller, "  call void @gc_f(i8%s %%ta, i16%s %%tb)\n  call void @gc_g(i1%s %%tc)\n", attrs(cfg.Params[0]), attrs(cfg.Params[1]), attrs(cgg.Params[0]))
	want := []int32{-3, 0xBEEF, 0, 0xC3, -3, 0xBEEF, 0}
	acc := "0"
	for i, wv := range want {
		fmt.Fprintf(&b.caller, "  %%q%d = getelementptr inbounds i32, ptr @gx_got, i64 %d\n  %%v%d = load i32, ptr %%q%d, align 4\n", i, i, i, i)
		fmt.Fprintf(&b.caller, "  %%n%d = icmp ne i32 %%v%d, %d\n  %%z%d = zext i1 %%n%d to i32\n  %%s%d = shl i32 %%z%d, %d\n  %%o%d = or i32 %s, %%s%d\n", i, i, wv, i, i, i, i, i, i, acc, i)
		acc = fmt.Sprintf("%%o%d", i)
	}
	fmt.Fprintf(&b.caller, "  ret i32 %s\n}\n\n", acc)
}

// support is the caller module's CRT-free support: entry, output, exit,
// memcpy and memset (LLVM may call them), and the byte comparison.
func (b *rtBuild) support() string {
	var s strings.Builder
	s.WriteString(`declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)

define dso_local ptr @memcpy(ptr %d, ptr %s, i64 %n) #2 {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %body ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %exit, label %body
body:
  %sp = getelementptr i8, ptr %s, i64 %i
  %b = load i8, ptr %sp
  %dp = getelementptr i8, ptr %d, i64 %i
  store i8 %b, ptr %dp
  %i1 = add i64 %i, 1
  br label %loop
exit:
  ret ptr %d
}

define dso_local ptr @memset(ptr %d, i32 %c, i64 %n) #2 {
entry:
  %v = trunc i32 %c to i8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %body ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %exit, label %body
body:
  %dp = getelementptr i8, ptr %d, i64 %i
  store i8 %v, ptr %dp
  %i1 = add i64 %i, 1
  br label %loop
exit:
  ret ptr %d
}

; rt_cmp is 1 when a and b differ in a byte that mask marks as data.
define internal i32 @rt_cmp(ptr %a, ptr %b, ptr %m, i64 %n) #2 {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %next ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %same, label %body
body:
  %ap = getelementptr i8, ptr %a, i64 %i
  %bp = getelementptr i8, ptr %b, i64 %i
  %mp = getelementptr i8, ptr %m, i64 %i
  %av = load volatile i8, ptr %ap
  %bv = load i8, ptr %bp
  %mv = load i8, ptr %mp
  %x = xor i8 %av, %bv
  %y = and i8 %x, %mv
  %bad = icmp ne i8 %y, 0
  %i1 = add i64 %i, 1
  br i1 %bad, label %differ, label %next
next:
  br label %loop
same:
  ret i32 0
differ:
  ret i32 1
}

@hexdigits = private constant [16 x i8] c"0123456789abcdef"

; rt_hex writes v as 8 hex digits and a newline.
define internal void @rt_hex(i32 %v) #2 {
entry:
  %buf = alloca [9 x i8], align 1
  br label %loop
loop:
  %i = phi i32 [ 0, %entry ], [ %i1, %loop ]
  %sh4 = mul i32 %i, 4
  %amt = sub i32 28, %sh4
  %d = lshr i32 %v, %amt
  %nib = and i32 %d, 15
  %nib64 = zext i32 %nib to i64
  %hp = getelementptr i8, ptr @hexdigits, i64 %nib64
  %h = load i8, ptr %hp
  %i64 = zext i32 %i to i64
  %bp = getelementptr i8, ptr %buf, i64 %i64
  store i8 %h, ptr %bp
  %i1 = add i32 %i, 1
  %done = icmp eq i32 %i1, 8
  br i1 %done, label %out, label %loop
out:
  %nl = getelementptr i8, ptr %buf, i64 8
  store i8 10, ptr %nl
  call void @rt_write(ptr %buf, i64 9)
  ret void
}

`)
	if b.target.OS == types.OSWindows {
		s.WriteString(`@_fltused = dso_local global i32 0

declare dllimport ptr @GetStdHandle(i32) nounwind
declare dllimport i32 @WriteFile(ptr, ptr, i32, ptr, ptr) nounwind
declare dllimport void @ExitProcess(i32) nounwind noreturn

module asm ".text"
module asm ".globl __chkstk"
module asm "__chkstk:"
module asm "  pushq %rcx"
module asm "  pushq %rax"
module asm "  cmpq $0x1000, %rax"
module asm "  leaq 24(%rsp), %rcx"
module asm "  jb 1f"
module asm "2:"
module asm "  subq $0x1000, %rcx"
module asm "  testq %rcx, (%rcx)"
module asm "  subq $0x1000, %rax"
module asm "  cmpq $0x1000, %rax"
module asm "  ja 2b"
module asm "1:"
module asm "  subq %rax, %rcx"
module asm "  testq %rcx, (%rcx)"
module asm "  popq %rax"
module asm "  popq %rcx"
module asm "  retq"

define internal void @rt_write(ptr %p, i64 %n) #2 {
entry:
  %written = alloca i32, align 4
  %h = call ptr @GetStdHandle(i32 -11)
  %n32 = trunc i64 %n to i32
  %ok = call i32 @WriteFile(ptr %h, ptr %p, i32 %n32, ptr %written, ptr null)
  ret void
}

define dso_local void @rt_start() #3 {
entry:
  %c = call i32 @rt_main()
  call void @ExitProcess(i32 %c)
  unreachable
}

`)
	} else {
		s.WriteString(`module asm ".text"
module asm ".globl _start"
module asm ".type _start,@function"
module asm "_start:"
module asm "  xorl %ebp, %ebp"
module asm "  andq $-16, %rsp"
module asm "  callq rt_start"
module asm "  ud2"

define internal void @rt_write(ptr %p, i64 %n) #2 {
entry:
  %w = call i64 asm sideeffect "syscall", "={rax},{rax},{rdi},{rsi},{rdx},~{rcx},~{r11},~{memory}"(i64 1, i64 1, ptr %p, i64 %n)
  ret void
}

define dso_local void @rt_start() #3 {
entry:
  %c = call i32 @rt_main()
  %c64 = zext i32 %c to i64
  call void asm sideeffect "syscall", "{rax},{rdi},~{rcx},~{r11},~{memory}"(i64 231, i64 %c64)
  unreachable
}

`)
	}
	return s.String()
}

// mainFn calls every row's caller @rt_call_<i> and reports each failing one
// as "FAIL <row> <bits>"; it returns 1 when any failed.
func (b *rtBuild) mainFn() string {
	var s strings.Builder
	s.WriteString("@fail_txt = private constant [5 x i8] c\"FAIL \"\n@done_txt = private constant [8 x i8] c\"rt-done\\0A\"\n")
	for i, n := range b.names {
		line := []byte(n + " ")
		fmt.Fprintf(&s, "@name_%d = private constant [%d x i8] %s\n", i, len(line), llBytes(line))
	}
	s.WriteString("\ndefine internal i32 @rt_main() #1 {\nentry:\n  %fails = alloca i32, align 4\n  store i32 0, ptr %fails\n  br label %row0\n")
	for i, n := range b.names {
		fmt.Fprintf(&s, "row%d:\n  %%c%d = call i32 @rt_call_%d()\n  %%bad%d = icmp ne i32 %%c%d, 0\n  br i1 %%bad%d, label %%fail%d, label %%row%d\n", i, i, i, i, i, i, i, i+1)
		fmt.Fprintf(&s, "fail%d:\n  call void @rt_write(ptr @fail_txt, i64 5)\n  call void @rt_write(ptr @name_%d, i64 %d)\n  call void @rt_hex(i32 %%c%d)\n", i, i, len(n)+1, i)
		fmt.Fprintf(&s, "  %%f%d = load i32, ptr %%fails\n  %%g%d = add i32 %%f%d, 1\n  store i32 %%g%d, ptr %%fails\n  br label %%row%d\n", i, i, i, i, i+1)
	}
	fmt.Fprintf(&s, "row%d:\n  call void @rt_write(ptr @done_txt, i64 8)\n  %%total = load i32, ptr %%fails\n  %%any = icmp ne i32 %%total, 0\n  %%code = zext i1 %%any to i32\n  ret i32 %%code\n}\n\n", len(b.names))
	s.WriteString("attributes #0 = { nounwind noinline }\nattributes #1 = { nounwind noinline }\nattributes #2 = { nounwind \"no-builtins\" }\nattributes #3 = { nounwind noreturn }\n")
	return s.String()
}
