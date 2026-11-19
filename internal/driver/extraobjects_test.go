// Why: trace output must not depend on where a package sits on disk, and
// benchmarks against C link precompiled objects into Mk executables.
// What: TestSitePathInTraceRows, TestExtraObjectsLinked and
// TestExtraObjectsInLinkKey.
// How: a trace site names its file package-relative (lower.Config.SitePath);
// Config.ExtraObjects links already compiled objects into every executable
// and enters the link key by content.

package driver

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// TestSitePathInTraceRows: a dev build of tests/programs/trace.mk named by
// its absolute path, from another directory (the corpus harness's way),
// prints every trace row with the package-relative file, lower's rows
// (fail and try) as well as codegen's (the scope's site). Before SitePath
// was set, lower's rows named V:/.../trace.mk.
func TestSitePathInTraceRows(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(programs, "trace.mk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			exe := buildProgram(t, abs, target, types.Dev, t.TempDir())
			for _, mode := range []string{"deep", "scope", "early"} {
				want, err := os.ReadFile(filepath.Join(programs, "trace-"+mode+"-dev.err"))
				if err != nil {
					t.Fatal(err)
				}
				_, errOut, code := runExe(t, target, exe, "", mode)
				got := strings.ReplaceAll(errOut, "\r\n", "\n")
				if code != 1 || got != strings.ReplaceAll(string(want), "\r\n", "\n") {
					t.Errorf("%s: exit %d, stderr\n%s\nwant exit 1 and\n%s", mode, code, got, want)
				}
				for _, line := range strings.Split(got, "\n") {
					if strings.HasPrefix(line, "    at ") && !strings.HasPrefix(line, "    at trace.mk:") {
						t.Errorf("%s: row %q does not name trace.mk package-relative", mode, line)
					}
				}
			}
		})
	}
}

// extPackage writes a package whose unsafe module ref declares the extern
// XAdd, which no Mk file defines, and whose Main prints ref.Add(2, 40).
func extPackage(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"ext\",\n    bins: [pkg.Bin(name: \"ext\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n" +
			"    deps: [],\n    c_sources: [],\n    c_imports: [],\n    asm_sources: [],\n    targets: [],\n" +
			"    unsafe_modules: [\"ref\"],\n			links: [],\n)\n",
		"ref/ref.mk": "extern XAdd(a: i64, b: i64): i64\n\nexport Add(a: i64, b: i64): i64 {\n" +
			"    unsafe \"XAdd is the test's extra object; it reads its two integers and nothing else\" {\n" +
			"        return XAdd(a, b)\n    }\n}\n",
		"main.mk": "import ref\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" +
			"    try Print(mut out, \"{a}\\n\", (a: ref.Add(2, 40)))\n}\n",
	}
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// extObject compiles an XAdd returning a + b + extra for target into
// path (clang on IR, as the packs are compiled).
func extObject(t *testing.T, tools *toolchain.Tools, target types.Target, path string, extra int) {
	t.Helper()
	ll := path + ".ll"
	text := "define i64 @XAdd(i64 %a, i64 %b) {\nentry:\n  %s = add i64 %a, %b\n" +
		"  %r = add i64 %s, " + strconv.Itoa(extra) + "\n  ret i64 %r\n}\n"
	if err := os.WriteFile(ll, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	err := tools.Compile(context.Background(), toolchain.CompileJob{Triple: target.LLVMTriple, Input: ll, Output: path, Opt: toolchain.O0})
	if err != nil {
		t.Fatal(err)
	}
}

// TestExtraObjectsLinked: with Config.ExtraObjects the extern resolves to
// the object's XAdd and the program prints 42, on both targets; the object
// is the link's last input, after the packs. Without it the same package
// does not link (E0001 from the linker), so the object is what defines it.
func TestExtraObjectsLinked(t *testing.T) {
	tools := needTools(t)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			dir := t.TempDir()
			pkgDir := filepath.Join(dir, "pkg")
			extPackage(t, pkgDir)
			obj := filepath.Join(dir, "xadd.o")
			extObject(t, tools, target, obj, 0)
			build := func(extra []string) (Result, []string, []string) {
				var objs []string
				s, err := NewSession(Config{Root: pkgDir, Target: target, Tools: tools, CacheDir: filepath.Join(dir, "cache"),
					ExtraObjects: extra, TestHooks: TestHooks{OnLink: func(j toolchain.LinkJob) { objs = j.Objects }}})
				if err != nil {
					t.Fatal(err)
				}
				r, paths, err := s.Build(filepath.Join(dir, "out", "ext"+exeSuffix(target)))
				if err != nil {
					t.Fatal(err)
				}
				return r, paths, objs
			}
			r, paths, objs := build([]string{obj})
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build with the object: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
			}
			if len(objs) < 2 || objs[len(objs)-1] != obj {
				t.Errorf("link objects %q: want the packs, then %s last", objs, obj)
			}
			out, errOut, code := runExe(t, target, paths[0], "")
			if code != 0 || out != "42\n" {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0 and \"42\\n\"", code, out, errOut)
			}
			r, _, _ = build(nil)
			if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0001 {
				t.Errorf("build without the object: want one E0001 (XAdd undefined), got\n%s", r.Report.Text(r.Sources))
			}
		})
	}
}

// TestExtraObjectsInLinkKey: mk run's executable is content-addressed by
// the link key. A second run with the same object links nothing; the
// object rebuilt in place with other bytes (XAdd now adds 1) relinks, and
// the run prints 43, not the stale 42.
func TestExtraObjectsInLinkKey(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	extPackage(t, pkgDir)
	obj := filepath.Join(dir, "xadd.obj")
	links := 0
	run := func(want string) {
		t.Helper()
		s, err := NewSession(Config{Root: pkgDir, Tools: tools, CacheDir: filepath.Join(dir, "cache"), Timings: io.Discard,
			ExtraObjects: []string{obj}, TestHooks: TestHooks{OnLink: func(toolchain.LinkJob) { links++ }}})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		r, status, err := s.Run(nil, RunIO{Stdout: &out, Stderr: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || status != 0 || out.String() != want {
			t.Fatalf("status %d stdout %q, want %q\n%s", status, out.String(), want, r.Report.Text(r.Sources))
		}
	}
	extObject(t, tools, HostTarget(), obj, 0)
	run("42\n")
	run("42\n")
	if links != 1 {
		t.Errorf("%d links over two identical runs, want 1", links)
	}
	extObject(t, tools, HostTarget(), obj, 1)
	run("43\n")
	if links != 2 {
		t.Errorf("%d links after the object changed, want 2", links)
	}
	if err := os.Remove(obj); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: pkgDir, Tools: tools, CacheDir: filepath.Join(dir, "cache"), ExtraObjects: []string{obj}})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0001 ||
		!strings.Contains(r.Report.Diags[0].Diag.Message, "extra object xadd.obj") {
		t.Errorf("a missing extra object: want one E0001 naming it, got\n%s", r.Report.Text(r.Sources))
	}
}
