// Why: MK_CACHE_VERIFY must repair a stale stored link even when Windows
// holds the file open or GC dropped its PDB, and never run the stale bytes.
// What: tests that under MK_CACHE_VERIFY a stale stored link is moved
// aside, never silently left in place, that mk run then runs a private fresh
// link, that a stored executable is compared even when GC aged its PDB out,
// and that mk test repairs a stored runner.
// How: two programs with different exit codes; one's stored executable is
// overwritten with the other's bytes, and holdRunning keeps it open while
// verifyRun builds and runs.

package driver

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// sleeperFiles is a program that exits with code when run with no
// argument and sleeps 60 s when run with one.
func sleeperFiles(code string) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "app", link_script: "", cpu: "", subsystem: "console")]`}),
		"app/app.mk": "import std.time\n\nexport Main(sys: &System): int {\n    if sys.args.Len() > 1 {\n" +
			"        sys.io.Sleep(time.Seconds(60)) else {\n        }\n        return 7\n    }\n    return " + code + "\n}\n",
	}
}

// holdRunning starts the executable p with one argument, so that it
// sleeps and, on Windows, p can be neither removed nor replaced while it
// runs. The process is killed when the test ends, before its temporary
// directories go.
func holdRunning(t *testing.T, p string) {
	t.Helper()
	cmd := exec.Command(p, "sleep")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	time.Sleep(2 * time.Second)
	if runtime.GOOS == "windows" {
		if err := os.Remove(p); err == nil {
			t.Fatal("a running executable could be removed: the test's premise fails")
		}
	}
}

// verifyRun is one mk run of cfg with no argument: the result, the exit
// code and whether the link ran.
func verifyRun(t *testing.T, cfg Config) (Result, int, bool) {
	t.Helper()
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

// TestVerifyStoredRunning: program A exits 6, B exits 9. A's stored
// executable holds B's bytes and runs (another terminal's mk run of the same
// link key runs that very file). Under MK_CACHE_VERIFY, mk build reports
// E0000 bin, places A's bytes and replaces the stored copy (a running image
// is moved aside, never removed), so the next plain build is a hit with A's
// bytes; mk run reports E0000 bin and runs A's fresh link, from a private
// directory, and replaces the stored copy too. A mutant whose verifyBin
// removes the file and ignores the error fails both on Windows.
func TestVerifyStoredRunning(t *testing.T) {
	tools := needTools(t)
	bdir := writeTree(t, sleeperFiles("9"))
	_, b, _ := verifyBuild(t, Config{Root: bdir, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache-b")}, filepath.Join(t.TempDir(), "b.exe"))
	t.Run("build", func(t *testing.T) {
		dir := writeTree(t, sleeperFiles("6"))
		cacheDir := filepath.Join(t.TempDir(), "cache")
		out := t.TempDir()
		cfg := Config{Root: dir, Tools: tools, CacheDir: cacheDir}
		_, cold, _ := verifyBuild(t, cfg, filepath.Join(out, "cold.exe"))
		stored := binFiles(t, cacheDir)
		if len(stored) != 1 {
			t.Fatalf("stored %v", stored)
		}
		if err := os.WriteFile(stored[0], b, 0o755); err != nil {
			t.Fatal(err)
		}
		holdRunning(t, stored[0])
		v := cfg
		v.CacheVerify = true
		r, exe, linked := verifyBuild(t, v, filepath.Join(out, "verify.exe"))
		sb, _ := os.ReadFile(stored[0])
		if !binVerifyDiag(r.Report.Diags) || !linked || !bytes.Equal(exe, cold) || !bytes.Equal(sb, cold) {
			t.Errorf("verify build while the stored copy runs: linked %v, output A %v, stored repaired %v\n%s",
				linked, bytes.Equal(exe, cold), bytes.Equal(sb, cold), r.Report.Text(r.Sources))
		}
		r, exe, linked = verifyBuild(t, cfg, filepath.Join(out, "after.exe"))
		if len(r.Report.Diags) != 0 || linked || !bytes.Equal(exe, cold) {
			t.Errorf("plain build after the verify: linked %v, output A %v, output B %v; want a hit with A's bytes", linked, bytes.Equal(exe, cold), bytes.Equal(exe, b))
		}
	})
	t.Run("run", func(t *testing.T) {
		dir := writeTree(t, sleeperFiles("6"))
		cacheDir := filepath.Join(t.TempDir(), "cache")
		cfg := Config{Root: dir, Tools: tools, CacheDir: cacheDir}
		r, code, _ := verifyRun(t, cfg)
		if len(r.Report.Diags) != 0 || code != 6 {
			t.Fatalf("cold run: exit %d\n%s", code, r.Report.Text(r.Sources))
		}
		stored := binFiles(t, cacheDir)
		if len(stored) != 1 {
			t.Fatalf("stored %v", stored)
		}
		good, _ := os.ReadFile(stored[0])
		if err := os.WriteFile(stored[0], b, 0o755); err != nil {
			t.Fatal(err)
		}
		holdRunning(t, stored[0])
		v := cfg
		v.CacheVerify = true
		r, code, linked := verifyRun(t, v)
		if !binVerifyDiag(r.Report.Diags) || code != 6 || !linked {
			t.Errorf("verify run while the stored copy runs: exit %d (A is 6, B is 9), linked %v\n%s", code, linked, r.Report.Text(r.Sources))
		}
		if sb, _ := os.ReadFile(stored[0]); !bytes.Equal(sb, good) {
			t.Errorf("verify run: the stored copy was not replaced (B's bytes %v)", bytes.Equal(sb, b))
		}
	})
}

// TestVerifyPDBAgedOut: GC ages a stored executable and its PDB apart.
// Under MK_CACHE_VERIFY a stored executable is compared even when its PDB
// went: one with other bytes is E0000 bin and is replaced, and the PDB is
// stored again, so the next plain build is a hit with the cold bytes; a
// sound one reports nothing, and mk build and mk run each store the PDB
// again. A mutant whose hit test needs the PDB under verify too fails the
// tampered case. COFF --debug only.
func TestVerifyPDBAgedOut(t *testing.T) {
	tools := needTools(t)
	if pkTarget() != types.X86_64Windows {
		t.Skipf("a PDB is a --debug COFF link's, and the host target is %s", pkTarget().Name)
	}
	setup := func(t *testing.T) (Config, string, []byte, string, string) {
		dir := writeTree(t, incPkg("x + 1"))
		cacheDir := filepath.Join(t.TempDir(), "cache")
		out := t.TempDir()
		cfg := Config{Root: dir, Tools: tools, CacheDir: cacheDir, Debug: true}
		_, cold, _ := verifyBuild(t, cfg, filepath.Join(out, "cold.exe"))
		var exe, pdb string
		for _, p := range binFiles(t, cacheDir) {
			switch filepath.Ext(p) {
			case ".exe":
				exe = p
			case ".pdb":
				pdb = p
			}
		}
		if exe == "" || pdb == "" {
			t.Fatalf("stored %v, want an executable and its PDB", binFiles(t, cacheDir))
		}
		if err := os.Remove(pdb); err != nil {
			t.Fatal(err)
		}
		return cfg, out, cold, exe, pdb
	}
	t.Run("tampered", func(t *testing.T) {
		cfg, out, cold, exe, pdb := setup(t)
		if err := os.WriteFile(exe, append(append([]byte(nil), cold...), "TAMPERED"...), 0o644); err != nil {
			t.Fatal(err)
		}
		v := cfg
		v.CacheVerify = true
		r, got, linked := verifyBuild(t, v, filepath.Join(out, "verify.exe"))
		sb, _ := os.ReadFile(exe)
		if !binVerifyDiag(r.Report.Diags) || !linked || !bytes.Equal(got, cold) || !bytes.Equal(sb, cold) || !isRegular(pdb) {
			t.Errorf("verify build: linked %v, output cold %v, stored repaired %v, PDB stored %v\n%s",
				linked, bytes.Equal(got, cold), bytes.Equal(sb, cold), isRegular(pdb), r.Report.Text(r.Sources))
		}
		r, got, linked = verifyBuild(t, cfg, filepath.Join(out, "after.exe"))
		if len(r.Report.Diags) != 0 || linked || !bytes.Equal(got, cold) {
			t.Errorf("plain build after the verify: linked %v, equal cold %v, TAMPERED %v; want a hit with the cold bytes", linked, bytes.Equal(got, cold), bytes.HasSuffix(got, []byte("TAMPERED")))
		}
	})
	t.Run("sound-build", func(t *testing.T) {
		cfg, out, cold, _, pdb := setup(t)
		v := cfg
		v.CacheVerify = true
		r, got, linked := verifyBuild(t, v, filepath.Join(out, "verify.exe"))
		if len(r.Report.Diags) != 0 || !linked || !bytes.Equal(got, cold) || !isRegular(pdb) {
			t.Errorf("verify build over a sound executable without its PDB: linked %v, equal %v, PDB stored %v\n%s", linked, bytes.Equal(got, cold), isRegular(pdb), r.Report.Text(r.Sources))
		}
		if _, _, linked := verifyBuild(t, cfg, filepath.Join(out, "after.exe")); linked {
			t.Error("plain build after the verify linked; want a hit")
		}
	})
	t.Run("sound-run", func(t *testing.T) {
		cfg, _, _, _, pdb := setup(t)
		v := cfg
		v.CacheVerify = true
		r, code, linked := verifyRun(t, v)
		if len(r.Report.Diags) != 0 || !linked || code != 6 || !isRegular(pdb) {
			t.Errorf("verify run over a sound executable without its PDB: exit %d, linked %v, PDB stored %v\n%s", code, linked, isRegular(pdb), r.Report.Text(r.Sources))
		}
	})
}

// TestVerifyStaleNotMovable: the stale stored executable cannot even be
// moved aside (this test holds it open: Windows refuses the rename then).
// The E0000 says the stored copy was not replaced and nothing is stored over
// it; mk build places the fresh bytes, and mk run runs the fresh link from
// its private directory, never the stale stored file (a mutant whose mk run
// keeps the stored path after a difference runs B). Once the file is
// released, a verify build replaces it. Windows only: elsewhere the rename
// of an open file succeeds.
func TestVerifyStaleNotMovable(t *testing.T) {
	tools := needTools(t)
	if runtime.GOOS != "windows" {
		t.Skipf("an open file blocks a rename on Windows only; the host is %s", runtime.GOOS)
	}
	bdir := writeTree(t, sleeperFiles("9"))
	_, b, _ := verifyBuild(t, Config{Root: bdir, Tools: tools, CacheDir: filepath.Join(t.TempDir(), "cache-b")}, filepath.Join(t.TempDir(), "b.exe"))
	dir := writeTree(t, sleeperFiles("6"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	out := t.TempDir()
	cfg := Config{Root: dir, Tools: tools, CacheDir: cacheDir}
	_, cold, _ := verifyBuild(t, cfg, filepath.Join(out, "cold.exe"))
	stored := binFiles(t, cacheDir)
	if len(stored) != 1 {
		t.Fatalf("stored %v", stored)
	}
	if err := os.WriteFile(stored[0], b, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(stored[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	notReplaced := func(r Result) bool {
		return binVerifyDiag(r.Report.Diags) && strings.Contains(fact(r.Report.Diags[0].Diag, "what"), "the stored copy was not replaced")
	}
	v := cfg
	v.CacheVerify = true
	r, exe, _ := verifyBuild(t, v, filepath.Join(out, "verify.exe"))
	sb, _ := os.ReadFile(stored[0])
	if !notReplaced(r) || !bytes.Equal(exe, cold) || !bytes.Equal(sb, b) {
		t.Errorf("verify build over an unmovable stale copy: output A %v, stored still B %v\n%s", bytes.Equal(exe, cold), bytes.Equal(sb, b), r.Report.Text(r.Sources))
	}
	r, code, _ := verifyRun(t, v)
	sb, _ = os.ReadFile(stored[0])
	if !notReplaced(r) || code != 6 || !bytes.Equal(sb, b) {
		t.Errorf("verify run over an unmovable stale copy: exit %d (A is 6, B is 9), stored still B %v\n%s", code, bytes.Equal(sb, b), r.Report.Text(r.Sources))
	}
	f.Close()
	r, _, _ = verifyBuild(t, v, filepath.Join(out, "verify2.exe"))
	sb, _ = os.ReadFile(stored[0])
	if !binVerifyDiag(r.Report.Diags) || notReplaced(r) || !bytes.Equal(sb, cold) {
		t.Errorf("verify build after the file was released: stored repaired %v\n%s", bytes.Equal(sb, cold), r.Report.Text(r.Sources))
	}
}

// TestVerifyTestRunner: mk test under MK_CACHE_VERIFY over a stored
// runner with other bytes reports E0000 bin, runs the fresh runner (the
// tests still pass), and repairs the store.
func TestVerifyTestRunner(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	src := "test \"adds\" {\n    Assert(1 + 2 == 3)\n}\n\nexport Main(sys: &System): int {\n    return 0\n}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	test := func(cfg Config) (Result, string, bool) {
		linked := false
		cfg.TestHooks.OnLink = func(toolchain.LinkJob) { linked = true }
		cfg.TestTimeout = time.Minute
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, rep, err := s.Test(TestOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if rep != nil {
			got = results(rep)
		}
		return r, got, linked
	}
	cfg := Config{Root: file, Tools: tools, CacheDir: cacheDir}
	r, got, _ := test(cfg)
	if len(r.Report.Diags) != 0 || !strings.HasSuffix(got, "adds pass") {
		t.Fatalf("cold test: %q\n%s", got, r.Report.Text(r.Sources))
	}
	stored := binFiles(t, cacheDir)
	if len(stored) != 1 {
		t.Fatalf("stored %v", stored)
	}
	good, _ := os.ReadFile(stored[0])
	if err := os.WriteFile(stored[0], append(append([]byte(nil), good...), "TAMPERED"...), 0o755); err != nil {
		t.Fatal(err)
	}
	v := cfg
	v.CacheVerify = true
	r, got, linked := test(v)
	sb, _ := os.ReadFile(stored[0])
	if !binVerifyDiag(r.Report.Diags) || !linked || !strings.HasSuffix(got, "adds pass") || !bytes.Equal(sb, good) {
		t.Errorf("mk test under verify over a tampered runner: linked %v, results %q, repaired %v\n%s", linked, got, bytes.Equal(sb, good), r.Report.Text(r.Sources))
	}
}
