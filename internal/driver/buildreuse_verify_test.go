// Why: the link store must never serve a link whose inputs its key does not
// cover, and MK_CACHE_VERIFY must catch a stored link that differs.
// What: tests that a bin with a linker script is not served from the link
// store, and that MK_CACHE_VERIFY compares a stored link (executable, PDB,
// ThinLTO build) with the link it makes, for mk build and mk run.
// How: verifyBuild keeps the diagnostics; the tests tamper with stored files
// and look for exactly one E0000 "cache verify" (binVerifyDiag).

package driver

import (
	"bytes"
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// verifyBuild is one mk build into out that keeps its diagnostics; it
// returns the result, the executable's bytes ("" with no path) and whether
// the link ran.
func verifyBuild(t *testing.T, cfg Config, out string) (Result, []byte, bool) {
	t.Helper()
	linked := false
	cfg.TestHooks.OnLink = func(toolchain.LinkJob) { linked = true }
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		skipKnownBlockers(t, r)
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	exe, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return r, exe, linked
}

// binVerifyDiag reports whether ds is exactly one E0000 of MK_CACHE_VERIFY
// over a stored link (where "cache verify", what "bin <key>: ...").
func binVerifyDiag(ds []diag.Emitted) bool {
	return len(ds) == 1 && fact(ds[0].Diag, "where") == "cache verify" && strings.HasPrefix(fact(ds[0].Diag, "what"), "bin ")
}

// elfSymbol is the value of the symbol name in the ELF image b.
func elfSymbol(t *testing.T, b []byte, name string) uint64 {
	t.Helper()
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == name {
			return s.Value
		}
	}
	t.Fatalf("no symbol %s", name)
	return 0
}

// TestScriptBinLinksEveryBuild: a freestanding bin's linker script
// INCLUDEs a second script by absolute path. The link key hashes the top
// script's bytes only, so a bin with a linker script is never served from
// the link store: after an edit of the included file a warm build links
// again and has the edited value, and nothing of the bin is stored under
// bin.
func TestScriptBinLinksEveryBuild(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	mem := filepath.ToSlash(filepath.Join(dir, "mem.ld"))
	files := map[string]string{
		"main.mk": "export extern MkEntry() {\n    var x := 0\n    for {\n        x += 0\n    }\n}\n",
		"kernel.ld": "ENTRY(MkEntry)\nINCLUDE \"" + mem + "\"\nSECTIONS\n{\n    . = 0x200000;\n    .text : { *(.text .text.*) }\n" +
			"    .rodata : { *(.rodata .rodata.*) }\n    .data : { *(.data .data.*) }\n    .bss : { *(.bss .bss.*) }\n}\n",
		"mem.ld": "ScriptMark = 0x1234;\n",
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "p", root: ".", link_script: "kernel.ld", cpu: "", subsystem: "console")]`}),
	}
	for p, text := range files {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	out := t.TempDir()
	cfg := Config{Root: dir, Target: types.X86_64Freestanding, CacheDir: cacheDir}
	r, exe, linked := verifyBuild(t, cfg, filepath.Join(out, "a"))
	if len(r.Report.Diags) > 0 || !linked || elfSymbol(t, exe, "ScriptMark") != 0x1234 {
		t.Fatalf("cold build: linked %v\n%s", linked, r.Report.Text(r.Sources))
	}
	if err := os.WriteFile(filepath.Join(dir, "mem.ld"), []byte("ScriptMark = 0x5678;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, exe, linked = verifyBuild(t, cfg, filepath.Join(out, "b"))
	if len(r.Report.Diags) > 0 {
		t.Fatalf("warm build:\n%s", r.Report.Text(r.Sources))
	}
	if got := elfSymbol(t, exe, "ScriptMark"); !linked || got != 0x5678 {
		t.Errorf("warm build after editing the INCLUDEd script: linked %v, ScriptMark %#x; want a link and 0x5678", linked, got)
	}
	if got := binFiles(t, cacheDir); len(got) != 0 {
		t.Errorf("a bin with a linker script stored %v under bin", got)
	}
}

// TestVerifyComparesStoredLink: MK_CACHE_VERIFY over a stored link
// links again and compares. Over a sound store it reports nothing; a stored
// executable with other bytes is E0000 where "cache verify", what "bin
// <key>", the build places the link's bytes, and the entry is replaced, so
// the next plain build is a hit with the cold build's bytes. For a dev
// build, --debug's PDB and a release (ThinLTO) build.
func TestVerifyComparesStoredLink(t *testing.T) {
	tools := needTools(t)
	for _, c := range []struct {
		prof  types.Profile
		debug bool
	}{{types.Dev, false}, {types.Dev, true}, {types.Release, false}} {
		debug := c.prof.String() + map[bool]string{false: "", true: " --debug"}[c.debug]
		dir := writeTree(t, incPkg("x + 1"))
		cacheDir := filepath.Join(t.TempDir(), "cache")
		out := t.TempDir()
		cfg := Config{Root: dir, Profile: c.prof, Tools: tools, CacheDir: cacheDir, Debug: c.debug}
		_, cold, _ := verifyBuild(t, cfg, filepath.Join(out, "cold.exe"))
		v := cfg
		v.CacheVerify = true
		r, exe, linked := verifyBuild(t, v, filepath.Join(out, "clean.exe"))
		if len(r.Report.Diags) != 0 || !linked || !bytes.Equal(exe, cold) {
			t.Fatalf("%s: verify over a sound store: linked %v, equal %v\n%s", debug, linked, bytes.Equal(exe, cold), r.Report.Text(r.Sources))
		}
		stored := binFiles(t, cacheDir)
		// Tamper with each stored file in turn (the executable, then the PDB).
		for _, victim := range stored {
			old, err := os.ReadFile(victim)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victim, append(append([]byte(nil), old...), "TAMPERED"...), 0o644); err != nil {
				t.Fatal(err)
			}
			r, exe, linked = verifyBuild(t, v, filepath.Join(out, "verify.exe"))
			if !binVerifyDiag(r.Report.Diags) || ExitStatus(r.Report) != 3 || !linked || !bytes.Equal(exe, cold) {
				t.Errorf("%s, %s tampered: verify build: linked %v, equal %v\n%s", debug, filepath.Base(victim), linked, bytes.Equal(exe, cold), r.Report.Text(r.Sources))
			}
			if b, err := os.ReadFile(victim); err != nil || !bytes.Equal(b, old) {
				t.Errorf("%s: %s not repaired (%v)", debug, filepath.Base(victim), err)
			}
			r, exe, linked = verifyBuild(t, cfg, filepath.Join(out, "after.exe"))
			if len(r.Report.Diags) != 0 || linked || !bytes.Equal(exe, cold) {
				t.Errorf("%s: plain build after the repair: linked %v, equal %v; want a hit with the cold bytes", debug, linked, bytes.Equal(exe, cold))
			}
		}
	}
}

// TestVerifyRunComparesStoredLink: mk run under MK_CACHE_VERIFY
// compares the stored executable it would run with a fresh link, and
// runs the fresh bytes after E0000 (before, a verify run ran the stored
// file unchecked).
func TestVerifyRunComparesStoredLink(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, incPkg("x + 1"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	run := func(cfg Config) (Result, int, bool) {
		linked := false
		cfg.TestHooks.OnLink = func(toolchain.LinkJob) { linked = true }
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, code, err := s.Run(nil, RunIO{Stdout: io.Discard, Stderr: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		return r, code, linked
	}
	cfg := Config{Root: dir, Tools: tools, CacheDir: cacheDir}
	r, code, _ := run(cfg)
	if len(r.Report.Diags) != 0 || code != 6 {
		skipKnownBlockers(t, r)
		t.Fatalf("cold run: exit %d\n%s", code, r.Report.Text(r.Sources))
	}
	stored := binFiles(t, cacheDir)
	if len(stored) != 1 {
		t.Fatalf("stored %v", stored)
	}
	good, _ := os.ReadFile(stored[0])
	v := cfg
	v.CacheVerify = true
	if r, code, linked := run(v); len(r.Report.Diags) != 0 || code != 6 || !linked {
		t.Fatalf("verify run over a sound store: exit %d, linked %v\n%s", code, linked, r.Report.Text(r.Sources))
	}
	if err := os.WriteFile(stored[0], append(append([]byte(nil), good...), "TAMPERED"...), 0o755); err != nil {
		t.Fatal(err)
	}
	r, code, linked := run(v)
	if !binVerifyDiag(r.Report.Diags) || code != 6 || !linked {
		t.Errorf("verify run over a tampered store: exit %d, linked %v\n%s", code, linked, r.Report.Text(r.Sources))
	}
	if b, err := os.ReadFile(stored[0]); err != nil || !bytes.Equal(b, good) {
		t.Errorf("the stored executable was not repaired (%v)", err)
	}
}
