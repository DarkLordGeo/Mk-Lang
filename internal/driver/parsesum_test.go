// Why: a warm check answers the import step and resolve's catalog from parse
// summaries instead of parsing files; a summary that differs from the file
// would change diagnostics silently.
// What: TestSummaryHitSameDiagnostics, TestCacheVerifyParseSummary
// and TestParseSummaryOfEveryStdFileRoundTrips.
// How: compares cold and warm diagnostics and Trace.FilesParsed, plants a
// wrong summary under MK_CACHE_VERIFY, and round-trips every std file.

package driver

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/iface"
	"mk/internal/syntax"
	"mk/internal/types"
)

// TestSummaryHitSameDiagnostics: the diagnostics the import step and
// resolve's catalog give from parse summaries alone equal the cold ones
// from ASTs, and the files they come from are not parsed on the warm
// check. The invalidation rows (invalidation_test.go) assert FilesParsed
// for clean builds.
func TestSummaryHitSameDiagnostics(t *testing.T) {
	for _, c := range []struct {
		name   string
		files  map[string]string
		code   string
		parsed []string // warm Trace.FilesParsed
	}{
		// The import step stops the build: no module runs stage 3, nothing
		// is parsed.
		{"E1102", map[string]string{"pkg.mk": manifest(nil),
			"ffi/a.mk": "export F(): int {\n    unsafe \"why\" {\n    }\n    return 1\n}\n\nstruct S {\n}\n\nunsafe impl Send for S {\n}\n"},
			"E1102", nil},
		{"E0202", map[string]string{"pkg.mk": manifest(nil), "util/u.mk": "export F(): int {\n    return 1\n}\n",
			"app/a.mk": "import utill\n\nexport G(): int {\n    return 2\n}\n"}, "E0202", nil},
		// The catalog's exporters of Helper come from lib's summary; app
		// has a diagnostic, is not stored, and is parsed again.
		{"E0201", map[string]string{"pkg.mk": manifest(nil), "lib/l.mk": "export Helper(): int {\n    return 1\n}\n",
			"app/a.mk": "export G(): int {\n    return Helper()\n}\n"}, "E0201", []string{"app/a.mk"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{Root: writeTree(t, c.files), CacheDir: t.TempDir(), Target: types.X86_64Windows}
			cold := frontCheck(t, cfg)
			if cs := codes(cold); !slices.Contains(cs, c.code) {
				t.Fatalf("cold codes %v, want %s", cs, c.code)
			}
			warm := frontCheck(t, cfg)
			if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
				t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold))
			}
			if !slices.Equal(warm.Trace.FilesParsed, c.parsed) {
				t.Errorf("warm parsed %q, want %q", warm.Trace.FilesParsed, c.parsed)
			}
		})
	}
}

// TestCacheVerifyParseSummary: under MK_CACHE_VERIFY every file is
// parsed and its summary compared with the stored one; a planted summary
// that differs is an E0000 "cache verify" naming the parse entry, and the
// build goes on with the recomputed summary.
func TestCacheVerifyParseSummary(t *testing.T) {
	cfg := Config{Root: writeTree(t, map[string]string{"pkg.mk": manifest(nil),
		"lib/l.mk": "export Helper(): int {\n    return 1\n}\n"}), CacheDir: t.TempDir(), Target: types.X86_64Windows}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cold := s.Check()
	clean(t, cold)
	verify := cfg
	verify.CacheVerify = true
	ok := frontCheck(t, verify)
	clean(t, ok)
	if !slices.Contains(ok.Trace.FilesParsed, "lib/l.mk") {
		t.Errorf("verify parsed %q, want lib/l.mk among them", ok.Trace.FilesParsed)
	}
	lib := s.pkgB.byName["lib"]
	key := s.w.front.parseSummaryKey(s.w, lib, lib.files[0])
	st, err := cache.Open(cfg.CacheDir, cache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The summary is in lib's parse index.
	ik := parseIndexKey(s.w, lib)
	raw, found, _ := st.Get(cache.KindManifest, ik)
	if !found {
		t.Fatal("no parse index stored for lib")
	}
	x, err := decodeParseIndex(raw)
	if err != nil {
		t.Fatal(err)
	}
	en, found := x.entries[key]
	if !found {
		t.Fatal("no parse summary stored for lib/l.mk")
	}
	p, err := iface.DecodeParseSummary(en.enc)
	if err != nil {
		t.Fatal(err)
	}
	p.Exports = append(p.Exports, "Planted")
	en.enc = iface.EncodeParseSummary(p)
	x.entries[key] = en
	if err := st.Put(cache.KindManifest, ik, encodeParseIndex(x)); err != nil {
		t.Fatal(err)
	}
	r := frontCheck(t, verify)
	if cs := codes(r); !slices.Equal(cs, []string{"E0000"}) {
		t.Fatalf("codes %v, want one E0000", cs)
	}
	if what := r.Report.Diags[0].Diag.Message; !strings.Contains(what, "parse") || !strings.Contains(what, "lib/l.mk") {
		t.Errorf("E0000 %q, want the parse entry of lib/l.mk", what)
	}
	// The verify build replaced the planted summary: the next one is clean.
	clean(t, frontCheck(t, verify))
}

// TestParseSummaryOfEveryStdFileRoundTrips: every std file's summary
// decodes to itself. Before addTok, std/derive/derive.mk's empty
// terminator tokens gave an anchor with a repeated start, which the
// decoder refuses, so its summary never hit.
func TestParseSummaryOfEveryStdFileRoundTrips(t *testing.T) {
	cfg, _ := Config{}.normalize()
	w := newWorld(cfg)
	n := 0
	err := filepath.WalkDir(filepath.Join("..", "..", "std"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f := &fileNode{src: w.sm.Add(filepath.ToSlash(p), raw), bodiless: true}
		f.ast, _ = syntax.ParseFile(f.src, w.in, syntax.Options{AllowBodiless: true})
		want := w.summarize(f)
		got, err := iface.DecodeParseSummary(iface.EncodeParseSummary(want))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		if !got.Bind(w.sm) || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the summary does not round-trip", p)
		}
		n++
		return nil
	})
	if err != nil || n < 50 {
		t.Fatalf("walked %d std files: %v", n, err)
	}
}
