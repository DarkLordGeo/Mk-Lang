// Why: the release protocol (types with an interface Free must be released)
// makes some older code E0701; mk fix --canonical must rewrite that code,
// and only where a rewrite provably compiles.
// What: releaseStd, TestReleaseProtocol, TestReleaseLeavesUnprovable and
// TestReleaseNeverTradesErrors.
// How: runs the release round over sample programs against a std that
// declares std.core's interface Free (a copy of std declares it when the
// live std does not), and recompiles each rewrite to prove it.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/format"
)

const freeIface = "export interface Free {\n    Free(self, a: &Alloc)\n}\n"

// releaseStd is a copy of std that declares std.core's interface Free.
func releaseStd(t *testing.T) string {
	declared := false
	dir := copyStd(t, nil, func(p string, b []byte) []byte {
		if strings.HasPrefix(p, "core/") && strings.Contains(string(b), "export interface Free {") {
			declared = true
		}
		return b
	})
	if !declared {
		p := filepath.Join(dir, "core", "mem_release.mk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(b, []byte("\n"+freeIface)...), 0o644); err != nil {
			t.Fatal(err)
		}
		// The two std.core test files whose older panic tests would be
		// E0701 once Free is declared: the copy drops them.
		for _, f := range []string{"tests_heap.mk", "tests_sync.mk"} {
			if err := os.Remove(filepath.Join(dir, "core", f)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

const releaseSrc = `export linear struct Tok {
    n: int
}

export linear struct Res {
    n: int
}

export Res.Free(self, a: &Alloc) {
    Res(n) := self
    _ = n
}

export Drop(a: &Alloc) {
    xs := HeapList[Tok].New(a)
    xs.Free(a)
}

export Put(a: &Alloc, m: mut HeapMap[int, Res], r: Res): OutOfMemory!void {
    try m.Set(a, 1, r)
}
`

func TestReleaseProtocol(t *testing.T) {
	t.Parallel()
	std := releaseStd(t)
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(releaseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, StdDir: std, CacheDir: t.TempDir()}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Check()
	if n := countCode(before, diag.E0701); n != 2 || len(before.Report.Diags) != 2 {
		t.Fatalf("want two E0701 before the round (Free and Set), got:\n%s", diagLines(before))
	}
	// Plain mk fix applies no compiles fix: the round is --canonical's.
	s, _ = NewSession(cfg)
	if res, err := s.Fix(FixOptions{DryRun: true}); err != nil || len(res.Rounds) != 0 {
		t.Fatalf("mk fix without --canonical: %v %+v", err, res.Rounds)
	}
	run := func() *FixResult {
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{Canonical: true})
		if err != nil || res.Stopped != "clean" || len(res.Final.Report.Diags) != 0 {
			t.Fatalf("mk fix --canonical: %v %q\n%s", err, res.Stopped, diagLines(res.Final))
		}
		return res
	}
	res := run()
	got, _ := os.ReadFile(file)
	release := 0
	for _, r := range res.Rounds {
		if r.Phase == releasePhase {
			release += len(r.Applied)
		}
	}
	if release != 2 {
		t.Errorf("release rewrites %d, want 2: %+v", release, res.Rounds)
	}
	wantLines(t, string(got),
		"    xs.FreeEmpty(a)\n",
		"    old := try m.Replace(a, 1, r)\n    if o := old {\n        o.Free(a)\n    }\n")
	if again := run(); len(again.Rounds) != 0 {
		t.Errorf("second run rewrote again: %+v\n%s", again.Rounds, got)
	}
	if out, err := format.Source("main.mk", got); err != nil || string(out) != string(got) {
		t.Errorf("not a fixed point of mk fmt (%v):\n%s", err, got)
	}
}

// TestReleaseLeavesUnprovable: a Set whose replaced value has no Free
// to call keeps its E0701 (the rewrite does not compile, so it is not
// kept), and mk check offers the FreeEmpty fix of a container's Free as
// compiles, recompiled.
func TestReleaseLeavesUnprovable(t *testing.T) {
	t.Parallel()
	std := releaseStd(t)
	src := "export linear struct Tok {\n    n: int\n}\n\nexport Put(a: &Alloc, m: mut HeapMap[int, Tok]): OutOfMemory!void {\n    try m.Set(a, 1, Tok(n: 2))\n}\n"
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, StdDir: std, CacheDir: t.TempDir()}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{Canonical: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rounds) != 0 || countCode(res.Final, diag.E0701) != 1 {
		t.Errorf("rounds %+v, final:\n%s", res.Rounds, diagLines(res.Final))
	}
	if got, _ := os.ReadFile(file); string(got) != src {
		t.Errorf("rewrote:\n%s", got)
	}

	drop := "export linear struct Tok {\n    n: int\n}\n\nexport Drop(a: &Alloc) {\n    xs := HeapList[Tok].New(a)\n    xs.Free(a)\n}\n"
	if err := os.WriteFile(file, []byte(drop), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CheckVerified()
	if err != nil {
		t.Fatal(err)
	}
	var fix *diag.Fix
	for _, e := range r.Report.Diags {
		for i, f := range e.Diag.Fixes {
			if e.Diag.Code == diag.E0701 && f.Edits[0].Text == "FreeEmpty" {
				fix = &e.Diag.Fixes[i]
			}
		}
	}
	if fix == nil || fix.Class != diag.Compiles {
		t.Errorf("E0701 of xs.Free offers no compiles FreeEmpty fix:\n%s", diagLines(r))
	}
}

// TestReleaseNeverTradesErrors: a Set whose value is a temporary is
// E0402 once its E0701 is gone (the temporary is lost on the error path,
// with Set as with Replace), so the rewrite would trade one error for
// another on the lines it wrote: the round keeps nothing and the file is
// unchanged. (A body that already names `old` is rewritten with `old2`:
// TestReleaseTwoSets.)
func TestReleaseNeverTradesErrors(t *testing.T) {
	t.Parallel()
	std := releaseStd(t)
	const head = "export linear struct Res {\n    n: int\n}\n\nexport Res.Free(self, a: &Alloc) {\n    Res(n) := self\n    _ = n\n}\n\n"
	for name, body := range map[string]string{
		"temporary": "export Put(a: &Alloc, m: mut HeapMap[int, Res]): OutOfMemory!void {\n    try m.Set(a, 1, Res(n: 2))\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			src := head + body
			file := filepath.Join(t.TempDir(), "main.mk")
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(Config{Root: file, StdDir: std, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Fix(FixOptions{Canonical: true})
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(file); string(got) != src || len(res.Rounds) != 0 {
				t.Errorf("rounds %+v rewrote:\n%s", res.Rounds, got)
			}
			if countCode(res.Final, diag.E0701) != 1 {
				t.Errorf("final:\n%s", diagLines(res.Final))
			}
		})
	}
}

func countCode(r Result, c diag.Code) int {
	n := 0
	for _, e := range r.Report.Diags {
		if e.Diag.Code == c {
			n++
		}
	}
	return n
}
