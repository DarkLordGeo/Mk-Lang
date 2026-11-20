// Why: some targets (aarch64-nutshell) are checked only; building for
// them must fail clearly instead of producing a broken executable.
// What: TestAArch64NotBuilt, TestAArch64LinuxELF and
// TestBuildRunTestVerified.
// How: mk build, run and test refuse such a target with E0202 reason
// not-built and write no executable, while mk check still type-checks for
// it; aarch64-linux, which is built, gives a static AArch64 ELF; the last
// test checks that build, run and test offer only fixes that recompile, as
// mk check does.

package driver

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

const t8Program = `Twice(n: int): int {
    return n * 2
}

export Main(sys: &System): WriteError!void {
    _ = Twice(2)
    var out := sys.Stdout()
    try Print(mut out, "hi\n")
}
`

func TestAArch64NotBuilt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "callers.mk")
	if err := os.WriteFile(file, []byte(t8Program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range []types.Target{types.Aarch64Nutshell} {
		t.Run(tg.Name, func(t *testing.T) {
			s, err := NewSession(Config{Root: file, Target: tg, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if r := s.Check(); len(r.Report.Diags) != 0 {
				t.Fatalf("mk check --target %s: %v", tg.Name, r.Report.Diags[0].Diag.Message)
			}
			out := filepath.Join(t.TempDir(), "a64.out")
			r, paths, err := s.Build(out)
			if err != nil || len(paths) != 0 || len(r.Report.Diags) != 1 {
				t.Fatalf("build: err %v, paths %v, %d diagnostics", err, paths, len(r.Report.Diags))
			}
			d := r.Report.Diags[0].Diag
			if d.Code != diag.E0202 || factText(d, "reason") != "not-built" || factText(d, "target") != tg.Name {
				t.Errorf("got %v %q %+v", d.Code, d.Message, d.Facts)
			}
			if _, err := os.Stat(out); err == nil {
				t.Errorf("%s was written", out)
			}
			// mk run and mk test reach the same refusal.
			rr, code, err := s.Run(nil, RunIO{})
			if err != nil || len(rr.Report.Diags) != 1 || rr.Report.Diags[0].Diag.Code != diag.E0202 || factText(rr.Report.Diags[0].Diag, "reason") != "not-built" {
				t.Errorf("run: err %v, exit %d, %s", err, code, diagLines(rr))
			}
			tr, _, err := s.Test(TestOptions{})
			if err != nil || len(tr.Report.Diags) != 1 || tr.Report.Diags[0].Diag.Code != diag.E0202 || factText(tr.Report.Diags[0].Diag, "reason") != "not-built" {
				t.Errorf("test: err %v, %s", err, diagLines(tr))
			}
		})
	}
}

// TestAArch64LinuxELF: dev and release, an aarch64-linux build is a static
// ELF64 executable for EM_AARCH64 with no interpreter, entered at _start in
// dev; a second build from an empty cache gives the same bytes. It is not
// run: the host is x86_64.
func TestAArch64LinuxELF(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "hi.mk")
	if err := os.WriteFile(file, []byte(t8Program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			exe := buildProgram(t, file, types.Aarch64Linux, prof, t.TempDir())
			f, err := elf.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if f.Class != elf.ELFCLASS64 || f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
				t.Errorf("class %v machine %v type %v, want ELFCLASS64 EM_AARCH64 ET_EXEC", f.Class, f.Machine, f.Type)
			}
			for _, p := range f.Progs {
				if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
					t.Errorf("program header %v in a static executable", p.Type)
				}
			}
			if prof == types.Dev {
				syms, err := f.Symbols()
				if err != nil {
					t.Fatal(err)
				}
				var start uint64
				for _, s := range syms {
					if s.Name == "_start" {
						start = s.Value
					}
				}
				if start == 0 || f.Entry != start {
					t.Errorf("entry %#x, _start %#x", f.Entry, start)
				}
			}
			again := buildProgram(t, file, types.Aarch64Linux, prof, t.TempDir())
			a, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(again)
			if err != nil {
				t.Fatal(err)
			}
			if string(a) != string(b) {
				t.Errorf("two builds differ: %d and %d bytes", len(a), len(b))
			}
		})
	}
}

func factText(d *diag.Diagnostic, key string) string {
	for _, f := range d.Facts {
		if f.Key == key {
			return f.Value.Str
		}
	}
	return ""
}

// TestBuildRunTestVerified: the compile errors of mk build, run and test
// carry their compiles fixes recompiled, as mk check's do: the fixprobe
// `try` in Main does not compile, so each command reports it as an
// alternative, not a fix.
func TestBuildRunTestVerified(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "main.mk")
	src := "import std.fs\n\nexport Main(sys: &System): WriteError!void {\n    s := fs.ReadText(sys.arena, sys.io, \"x.txt\")\n    var out := sys.Stdout()\n    try Print(mut out, \"{s}\", (s))\n}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(what string, r Result) {
		t.Helper()
		if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0802 {
			t.Fatalf("%s: want one E0802:\n%s", what, diagLines(r))
		}
		if d := r.Report.Diags[0].Diag; len(d.Fixes) != 0 || len(d.Alternatives) != 3 {
			t.Errorf("%s: fixes %+v alternatives %q", what, d.Fixes, d.Alternatives)
		}
	}
	session := func() *Session {
		s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	r, _, err := session().Build(filepath.Join(t.TempDir(), "a.exe"))
	if err != nil {
		t.Fatal(err)
	}
	check("build", r)
	r, _, err = session().Run(nil, RunIO{})
	if err != nil {
		t.Fatal(err)
	}
	check("run", r)
	r, _, err = session().Test(TestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	check("test", r)
}
