// Why: a callee's clause over const generics should be judged over the
// finite set of values the caller's own clauses allow, not restated.
// What: TestSimdWithOverRealStd over the repository's std.simd, and
// TestPoolFreeOfLinearT over a stand-in std.mem.
// How: `Vec[u8, N].With(i, x)` in a body generic over N checks with no
// restated `i >= 0 && i < N` (utf8.mk's shape), cold and warm, and an index
// out of range for one allowed N is E0706 at the call.

package driver

import (
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
)

func TestSimdWithOverRealStd(t *testing.T) {
	stdDir := realStdWith(t, nil)
	const lanes = "    requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64\n"
	cases := []struct{ name, body, want string }{
		{"utf8 shape", lanes + "    requires N == 16 || N == 32 || N == 64 {\n    return v.With(0, 1).With(2, 3).With(15, 4)\n}\n", ""},
		{"every lane count", lanes + "    {\n    return v.With(1, 1)\n}\n", ""},
		{"out of range for N == 2", lanes + "    requires N == 2 || N == 4 {\n    return v.With(3, 1)\n}\n", "3 >= 0 && 3 < N"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import std.simd\n\nexport G[const N: int](v: simd.Vec[u8, N]): simd.Vec[u8, N]\n" + tc.body
			root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": src}))
			cfg := Config{Root: root, StdDir: stdDir, CacheDir: filepath.Join(t.TempDir(), "cache")}
			// Cold, then warm: std.simd's With then comes from the module
			// cache, so its clause is a Load-decoded item's Contract.Text.
			for _, run := range []string{"cold", "warm"} {
				s, err := NewSession(cfg)
				if err != nil {
					t.Fatal(err)
				}
				r := s.Check()
				if tc.want == "" {
					clean(t, r)
					continue
				}
				text := r.Report.Text(r.Sources)
				if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0706 || !strings.Contains(text, tc.want) {
					t.Fatalf("%s: want one E0706 naming %q, got:\n%s", run, tc.want, text)
				}
			}
		})
	}
}

// TestPoolFreeOfLinearT: `Pool[T].Free` of a closed linear T is E0701
// at the call; a plain T checks clean.
// An open T is judged in the generic: E0701 unless a bound makes
// it not linear (T: Plain). The std is the
// stand-in with a std.mem holding Pool's shape, so the test does not
// follow std's live edits.
func TestPoolFreeOfLinearT(t *testing.T) {
	files := fakeStdFiles()
	files["mem/pool.mk"] = "export struct Handle {\n    i: u32\n}\n\n" +
		"export struct Pool[T] {\n    n: int\n}\n\n" +
		"export Pool[T].Free(mut self, h: Handle): bool {\n    return false\n}\n"
	stdDir := writeTree(t, files)
	const decls = "import std.mem\n\nexport linear struct L {\n    x: int\n}\n\n"
	cases := []struct {
		name, src string
		bad       bool
	}{
		{"linear", decls + "export F(p: mut mem.Pool[L], h: mem.Handle): bool {\n    return p.Free(h)\n}\n", true},
		{"plain", decls + "export F(p: mut mem.Pool[int], h: mem.Handle): bool {\n    return p.Free(h)\n}\n", false},
		// An open T is judged in the generic, from its bounds: an
		// unbounded T may be linear (F[L] would panic in Free), T: Plain is not.
		{"open", decls + "export F[T](p: mut mem.Pool[T], h: mem.Handle): bool {\n    return p.Free(h)\n}\n", true},
		{"open, plain", decls + "export F[T: Plain](p: mut mem.Pool[T], h: mem.Handle): bool {\n    return p.Free(h)\n}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": tc.src}))
			s, err := NewSession(Config{Root: root, StdDir: stdDir, CacheDir: filepath.Join(t.TempDir(), "cache")})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			if !tc.bad {
				clean(t, r)
				return
			}
			text := r.Report.Text(r.Sources)
			if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0701 || !strings.Contains(text, "a.mk:8:14") || !strings.Contains(text, "not linear") {
				t.Fatalf("want one E0701 at 8:14 with bound not linear, got:\n%s", text)
			}
		})
	}
}
