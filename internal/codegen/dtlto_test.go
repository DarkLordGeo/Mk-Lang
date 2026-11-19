// Why: lld-link's own ThinLTO backend skips the SLP vectorizer, so a Windows
// release link runs the backends through clang (unless
// LinkJob.InProcessLTO), as ld.lld does, and caches their objects.
// What: TestDTLTOVectorizesSLP, TestLTOUnrollThreshold (every backend gets
// toolchain's full-unroll threshold ltoUnroll), and the cache key tests
// TestDTLTOCacheKey and TestDTLTOImportedModuleChanges.
// How: links small bitcode modules through toolchain.Link and reads the
// executables; codegen may import toolchain in tests only.

package codegen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/toolchain"
)

// dtKern is a straight-line pair of multiplies that the SLP vectorizer
// turns into one mulpd; noinline keeps it a function of its own.
const dtKern = `target triple = "x86_64-pc-windows-msvc"

define dso_local void @kern(ptr noalias %c, ptr noalias %a, ptr noalias %b) #0 {
  %a0 = load double, ptr %a, align 8
  %b0 = load double, ptr %b, align 8
  %m0 = fmul double %a0, %b0
  %a1p = getelementptr inbounds i8, ptr %a, i64 8
  %b1p = getelementptr inbounds i8, ptr %b, i64 8
  %c1p = getelementptr inbounds i8, ptr %c, i64 8
  %a1 = load double, ptr %a1p, align 8
  %b1 = load double, ptr %b1p, align 8
  %m1 = fmul double %a1, %b1
  store double %m0, ptr %c, align 8
  store double %m1, ptr %c1p, align 8
  ret void
}

attributes #0 = { noinline nounwind "target-cpu"="x86-64-v2" }
`

// dtMain is the entry: kern over values read through volatile loads (so
// nothing folds), exit code 3*7 + 5*11 = 76.
const dtMain = `target triple = "x86_64-pc-windows-msvc"

@a = dso_local global [2 x double] [double 3.0, double 5.0]
@b = dso_local global [2 x double] [double 7.0, double 11.0]

declare void @kern(ptr, ptr, ptr)

define dso_local i32 @mk_start() #0 {
  %la = alloca [2 x double], align 16
  %lb = alloca [2 x double], align 16
  %lc = alloca [2 x double], align 16
  %a0 = load volatile double, ptr @a, align 8
  %a1 = load volatile double, ptr getelementptr inbounds (i8, ptr @a, i64 8), align 8
  %b0 = load volatile double, ptr @b, align 8
  %b1 = load volatile double, ptr getelementptr inbounds (i8, ptr @b, i64 8), align 8
  store double %a0, ptr %la, align 16
  %la1 = getelementptr inbounds i8, ptr %la, i64 8
  store double %a1, ptr %la1, align 8
  store double %b0, ptr %lb, align 16
  %lb1 = getelementptr inbounds i8, ptr %lb, i64 8
  store double %b1, ptr %lb1, align 8
  call void @kern(ptr %lc, ptr %la, ptr %lb)
  %c0 = load double, ptr %lc, align 16
  %lc1 = getelementptr inbounds i8, ptr %lc, i64 8
  %c1 = load double, ptr %lc1, align 8
  %s = fadd double %c0, %c1
  %r = fptosi double %s to i32
  ret i32 %r
}

attributes #0 = { nounwind "target-cpu"="x86-64-v2" }
`

// dtSupport is a native (not bitcode) object, as the support pack is: it
// defines _fltused, which a COFF object that uses floating point references
// only after code generation, so a bitcode definition would be dropped.
const dtSupport = `target triple = "x86_64-pc-windows-msvc"

@_fltused = dso_local global i32 0
`

// TestDTLTOVectorizesSLP links two ThinLTO bitcode modules for Windows
// through toolchain.Link and checks that the SLP vectorizer ran (a mulpd in
// the executable; lld-link's own backend, InProcessLTO, leaves two mulsd,
// which the test checks too, so it notices when LLVM closes the gap), that
// the program runs, that a second link hits the backend cache and gives the
// same bytes, and that the cache is pruned to its size bound.
func TestDTLTOVectorizesSLP(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a COFF link that runs: Windows hosts only")
	}
	bin := needLLVM(t)
	tools, err := toolchain.Discover(toolchain.Options{LLVMDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var objs []string
	for _, m := range []struct {
		name, src string
		lto       bool
	}{{"main", dtMain, true}, {"kern", dtKern, true}, {"support", dtSupport, false}} {
		ll := filepath.Join(dir, m.name+".ll")
		if err := os.WriteFile(ll, []byte(m.src), 0o644); err != nil {
			t.Fatal(err)
		}
		obj := filepath.Join(dir, m.name+".o")
		if err := tools.Compile(ctx, toolchain.CompileJob{Triple: "x86_64-pc-windows-msvc", Input: ll, Output: obj,
			Opt: toolchain.O3, LTO: m.lto, CPU: "x86-64-v2"}); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, obj)
	}
	cache := filepath.Join(dir, "thinlto")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(out string, size int64) []byte {
		t.Helper()
		job := toolchain.LinkJob{Format: toolchain.COFF, Output: out, Objects: objs, Release: true, LTO: true,
			LTOCacheDir: cache, LTOCacheSize: size, Jobs: 2}
		if err := tools.Link(ctx, job); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	entries := func() int {
		t.Helper()
		ents, err := os.ReadDir(cache)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "mkdt-") {
				n++
			}
		}
		return n
	}
	exe1 := filepath.Join(dir, "a1.exe")
	b1 := link(exe1, 1<<30)
	if n := entries(); n != 2 {
		t.Errorf("after the first link the cache holds %d backend objects, want 2", n)
	}
	dis := runTool(t, tool(bin, "llvm-objdump"), "-d", exe1)
	if !strings.Contains(dis, "mulpd") {
		t.Errorf("no mulpd in the executable: the SLP vectorizer did not run\n%s", dis)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer rcancel()
	cmd := exec.CommandContext(rctx, exe1)
	err = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 76 {
		t.Errorf("exit code %d (%v), want 76", code, err)
	}
	// A second link: every backend a cache hit, the same bytes.
	stamp := time.Now().Add(-time.Hour)
	ents, _ := os.ReadDir(cache)
	for _, e := range ents {
		os.Chtimes(filepath.Join(cache, e.Name()), stamp, stamp)
	}
	b2 := link(filepath.Join(dir, "a2.exe"), 1<<30)
	if !bytes.Equal(b1, b2) {
		t.Error("the second link gave different bytes")
	}
	if n := entries(); n != 2 {
		t.Errorf("after the second link the cache holds %d backend objects, want 2", n)
	}
	ents, _ = os.ReadDir(cache)
	for _, e := range ents {
		if info, err := e.Info(); err == nil && strings.HasPrefix(e.Name(), "mkdt-") && !info.ModTime().After(stamp) {
			t.Errorf("%s was not touched by its hit", e.Name())
		}
	}
	// A bound of one byte prunes every entry after the link.
	link(filepath.Join(dir, "a3.exe"), 1)
	if n := entries(); n != 0 {
		t.Errorf("a 1-byte bound left %d backend objects", n)
	}
	// The negative control: lld-link's own backend does not vectorize.
	in := filepath.Join(dir, "a4.exe")
	if err := tools.Link(ctx, toolchain.LinkJob{Format: toolchain.COFF, Output: in, Objects: objs, Release: true,
		LTO: true, InProcessLTO: true}); err != nil {
		t.Fatal(err)
	}
	if dis := runTool(t, tool(bin, "llvm-objdump"), "-d", in); strings.Contains(dis, "mulpd") {
		t.Errorf("lld-link's own backend vectorized: thinLink may no longer be needed (link.go)\n%s", dis)
	}
}

// unrollKern is a loop of 8 iterations whose body is a chain of 22 multiply
// and add pairs carried from one iteration to the next, so nothing
// vectorizes it. Calibrated with clang 21.1.8 -O3: it stays rolled at a
// full-unroll threshold of 300 (LLVM's -O3 default) and 325 and unrolls
// fully from 350; ltoUnroll is 400. Unrolled it has 8 * 22 mulsd.
func unrollKern(triple string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "target triple = %q\n\n", triple)
	b.WriteString("define dso_local double @kern(ptr noalias %p, double %s) #0 {\nentry:\n  br label %loop\nloop:\n")
	b.WriteString("  %i = phi i64 [ 0, %entry ], [ %i1, %loop ]\n  %x0 = phi double [ %s, %entry ], [ %x22, %loop ]\n")
	b.WriteString("  %a = getelementptr inbounds double, ptr %p, i64 %i\n")
	for k := 1; k <= 22; k++ {
		fmt.Fprintf(&b, "  %%m%d = fmul double %%x%d, 1.0001\n  %%x%d = fadd double %%m%d, 0.5\n", k, k-1, k, k)
	}
	b.WriteString("  store double %x22, ptr %a, align 8\n  %i1 = add nuw nsw i64 %i, 1\n")
	b.WriteString("  %c = icmp ult i64 %i1, 8\n  br i1 %c, label %loop, label %exit\nexit:\n  ret double %x22\n}\n\n")
	b.WriteString("attributes #0 = { noinline nounwind \"target-cpu\"=\"x86-64-v2\" }\n")
	return b.String()
}

// unrollMain calls kern on a local buffer from the entry symbol. The
// executables are only disassembled, never run.
func unrollMain(triple, entry string) string {
	return fmt.Sprintf(`target triple = %q

@seed = dso_local global double 1.5

declare double @kern(ptr, double)

define dso_local i32 @%s() #0 {
  %%buf = alloca [8 x double], align 16
  %%s = load volatile double, ptr @seed, align 8
  %%r = call double @kern(ptr %%buf, double %%s)
  store volatile double %%r, ptr @seed, align 8
  ret i32 0
}

attributes #0 = { nounwind "target-cpu"="x86-64-v2" }
`, triple, entry)
}

// TestLTOUnrollThreshold links unrollKern through toolchain.Link in each
// ThinLTO path (COFF through thinLink and through lld-link's own backend,
// ELF through ld.lld; nothing runs, so no WSL is needed) and checks that the
// loop was unrolled fully: every backend runs with toolchain's ltoUnroll.
func TestLTOUnrollThreshold(t *testing.T) {
	bin := needLLVM(t)
	tools, err := toolchain.Discover(toolchain.Options{LLVMDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, c := range []struct {
		name, triple, entry string
		format              toolchain.Format
		inProcess           bool
	}{
		{"coff-thinlink", "x86_64-pc-windows-msvc", "mk_start", toolchain.COFF, false},
		{"coff-in-process", "x86_64-pc-windows-msvc", "mk_start", toolchain.COFF, true},
		{"elf", "x86_64-unknown-linux-gnu", "_start", toolchain.ELF, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			srcs := []struct {
				name, src string
				lto       bool
			}{{"main", unrollMain(c.triple, c.entry), true}, {"kern", unrollKern(c.triple), true}}
			if c.format == toolchain.COFF {
				srcs = append(srcs, struct {
					name, src string
					lto       bool
				}{"support", dtSupport, false})
			}
			var objs []string
			for _, m := range srcs {
				ll := filepath.Join(dir, m.name+".ll")
				if err := os.WriteFile(ll, []byte(m.src), 0o644); err != nil {
					t.Fatal(err)
				}
				obj := filepath.Join(dir, m.name+".o")
				if err := tools.Compile(ctx, toolchain.CompileJob{Triple: c.triple, Input: ll, Output: obj,
					Opt: toolchain.O3, LTO: m.lto, CPU: "x86-64-v2"}); err != nil {
					t.Fatal(err)
				}
				objs = append(objs, obj)
			}
			out := filepath.Join(dir, "a.out")
			if err := tools.Link(ctx, toolchain.LinkJob{Format: c.format, Output: out, Objects: objs, Release: true,
				LTO: true, InProcessLTO: c.inProcess}); err != nil {
				t.Fatal(err)
			}
			dis := runTool(t, tool(bin, "llvm-objdump"), "-d", out)
			if n := strings.Count(dis, "mulsd"); n < 8*22 {
				t.Errorf("%d mulsd, want at least %d: the loop was not unrolled fully (ltoUnroll)\n%s", n, 8*22, dis)
			}
		})
	}
}

// TestDTLTOCacheKey: toolchain.ThinCacheKey,
// the key of one thinLink backend object, changes when any one of its
// inputs changes (clang's version line, an argument, the module's bytes, its
// index file's bytes, its .imports list, an imported module's bytes), reads
// exactly the files it names (an imported module by the name the .imports
// file gives, absolute or not), and fails on an imported module it cannot
// read rather than leaving it out of the key.
func TestDTLTOCacheKey(t *testing.T) {
	const ver = "clang version 21.1.8"
	files := map[string][]byte{"m0000.o": []byte("main bitcode"), "m0000.o.thinlto.bc": []byte("main index"),
		"m0001.o": []byte("kern bitcode"), `C:\w\m0002.o`: []byte("lib bitcode")}
	imports := []byte("m0001.o\r\n" + `C:\w\m0002.o` + "\r\n")
	args := []string{"--target=x86_64-pc-windows-msvc", "-O3", "-x", "ir", "m0000.o", "-fthinlto-index=m0000.o.thinlto.bc", "-mllvm", "-unroll-threshold=400"}
	var asked []string
	key := func(version string, args []string, imports []byte, files map[string][]byte) string {
		t.Helper()
		asked = asked[:0]
		k, err := toolchain.ThinCacheKey(version, args, "m0000.o", "m0000.o.thinlto.bc", imports, func(name string) ([]byte, error) {
			asked = append(asked, name)
			b, ok := files[name]
			if !ok {
				return nil, fmt.Errorf("no file %s", name)
			}
			return b, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	with := func(name, text string) map[string][]byte {
		m := map[string][]byte{}
		for k, v := range files {
			m[k] = v
		}
		m[name] = []byte(text)
		return m
	}
	argsWith := func(i int, a string) []string {
		out := append([]string(nil), args...)
		out[i] = a
		return out
	}
	base := key(ver, args, imports, files)
	if want := []string{"m0000.o", "m0000.o.thinlto.bc", "m0001.o", `C:\w\m0002.o`}; !reflect.DeepEqual(asked, want) {
		t.Errorf("read %q, want %q", asked, want)
	}
	if again := key(ver, args, imports, files); again != base {
		t.Errorf("the same inputs gave two keys: %s, %s", base, again)
	}
	seen := map[string]string{base: "base"}
	for _, c := range []struct{ what, key string }{
		{"version line", key("clang version 21.1.7", args, imports, files)},
		{"an argument", key(ver, argsWith(7, "-unroll-threshold=300"), imports, files)},
		{"an argument added", key(ver, append(append([]string(nil), args...), "-g"), imports, files)},
		{"arguments split differently", key(ver, argsWith(6, "-mllvm -unroll-threshold=400")[:7], imports, files)},
		{"the module", key(ver, args, imports, with("m0000.o", "main bitcode, edited"))},
		{"the index file", key(ver, args, imports, with("m0000.o.thinlto.bc", "main index, edited"))},
		{"the imports list", key(ver, args, []byte("m0001.o\r\n"), files)},
		{"an imported module", key(ver, args, imports, with("m0001.o", "kern bitcode, edited"))},
		{"the other imported module", key(ver, args, imports, with(`C:\w\m0002.o`, "lib bitcode, edited"))},
	} {
		if prev, ok := seen[c.key]; ok {
			t.Errorf("a change of %s keeps the key of %s", c.what, prev)
		}
		seen[c.key] = c.what
	}
	// An imported module that cannot be read is an error, never a key
	// without it.
	if _, err := toolchain.ThinCacheKey(ver, args, "m0000.o", "m0000.o.thinlto.bc", []byte("m0009.o\n"), func(name string) ([]byte, error) {
		if b, ok := files[name]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("no file %s", name)
	}); err == nil {
		t.Error("an unreadable imported module gave a key")
	}
}

// dtltoLib is a module whose small function main's backend imports and
// inlines; dtltoMain returns 40 + bias() as the exit code.
func dtltoLib(k int) string {
	return fmt.Sprintf(`target triple = "x86_64-pc-windows-msvc"

define dso_local i32 @bias() #0 {
  ret i32 %d
}

attributes #0 = { nounwind "target-cpu"="x86-64-v2" }
`, k)
}

const dtltoMain = `target triple = "x86_64-pc-windows-msvc"

@seed = dso_local global i32 40

declare i32 @bias()

define dso_local i32 @mk_start() #0 {
  %s = load volatile i32, ptr @seed, align 4
  %b = call i32 @bias()
  %r = add i32 %s, %b
  ret i32 %r
}

attributes #0 = { nounwind "target-cpu"="x86-64-v2" }
`

// TestDTLTOImportedModuleChanges is that key end to end, through the
// .imports files lld-link really writes: main's backend imports and inlines
// bias() from the library module, so when only the library changes, main's
// cached object is stale unless its key covers the imported module. The
// relink must give the new exit code and miss both backends; a new clang
// version line misses every backend; an unchanged relink hits every one.
// The backend arguments are fixed by link.go, so their part of the key is
// TestDTLTOCacheKey's.
func TestDTLTOImportedModuleChanges(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a COFF link that runs: Windows hosts only")
	}
	bin := needLLVM(t)
	tools, err := toolchain.Discover(toolchain.Options{LLVMDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	compile := func(name, src string) string {
		t.Helper()
		ll := filepath.Join(dir, name+".ll")
		if err := os.WriteFile(ll, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		obj := filepath.Join(dir, name+".o")
		if err := tools.Compile(ctx, toolchain.CompileJob{Triple: "x86_64-pc-windows-msvc", Input: ll, Output: obj,
			Opt: toolchain.O3, LTO: true, CPU: "x86-64-v2"}); err != nil {
			t.Fatal(err)
		}
		return obj
	}
	objs := []string{compile("main", dtltoMain), compile("lib", dtltoLib(2))}
	cache := filepath.Join(dir, "thinlto")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	step := 0
	linkRun := func(what string, wantEntries, wantExit int) {
		t.Helper()
		step++
		exe := filepath.Join(dir, fmt.Sprintf("a%d.exe", step))
		if err := tools.Link(ctx, toolchain.LinkJob{Format: toolchain.COFF, Output: exe, Objects: objs, Release: true,
			LTO: true, LTOCacheDir: cache, LTOCacheSize: 1 << 30, Jobs: 2}); err != nil {
			t.Fatal(err)
		}
		ents, err := os.ReadDir(cache)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "mkdt-") {
				n++
			}
		}
		if n != wantEntries {
			t.Errorf("%s: the cache holds %d backend objects, want %d", what, n, wantEntries)
		}
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		cmd := exec.CommandContext(rctx, exe)
		err = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != wantExit {
			t.Errorf("%s: exit code %d (%v), want %d", what, code, err, wantExit)
		}
	}
	linkRun("first link", 2, 42)
	linkRun("unchanged relink", 2, 42)
	objs[1] = compile("lib", dtltoLib(5))
	linkRun("only the imported module changed", 4, 45)
	tools.Version.Line += " (another build)"
	linkRun("a new clang version line", 6, 45)
	linkRun("unchanged relink after it", 6, 45)
}
