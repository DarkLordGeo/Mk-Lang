// Why: zdec is built from inputs outside the repository and judges std's
// zstd tests, so its wiring, pins and self-check must be tested themselves.
// What: tests that MK_ZDEC reaches every go test of ./std and nothing else, a
// faked gate builds nothing, the step skips without its inputs and fails on a
// wrong pin, the MSVC search takes the newest complete directories, and
// (with the evidence and clang present) the real build passes and is reused.
// How: g.runCmd fakes commands; this test binary doubles as a fake zdec or
// clang when run without -test. flags (fakeZdecEnv).

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// withZdec records path as g's zdec for the test's length.
func withZdec(t *testing.T, g *gate, path string) {
	t.Helper()
	zdecs.Lock()
	zdecs.m[g] = &zdecRun{path: path}
	zdecs.Unlock()
	forget(t, g)
}

// zdecOf is the MK_ZDEC entries of env.
func zdecOf(env []string) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); sameKey(k, "MK_ZDEC") {
			out = append(out, kv)
		}
	}
	return out
}

// TestZdecReachesStdOnly: with a zdec recorded, the test step's ./std
// run, the Linux step's ./std run and a fuzz command of ./std get MK_ZDEC
// (in the child's environment and in the step's record); the other
// packages' runs, the probe and go list do not; and the caller's stale
// MK_ZDEC reaches none of them.
func TestZdecReachesStdOnly(t *testing.T) {
	t.Setenv("MK_ZDEC", `C:\stale\zdec.exe`)
	g, calls := fakeGate(t, "quick", stepAnswer)
	withZdec(t, g, `W:\zdec\k\zdec.exe`)
	want := []string{`MK_ZDEC=W:\zdec\k\zdec.exe`}
	g.testStep()
	if len(*calls) != 3 {
		t.Fatalf("test step calls: %q", *calls)
	}
	for _, c := range *calls {
		got := zdecOf(c.env)
		if slices.Contains(c.argv, stdPkg) != slices.Equal(got, want) || (!slices.Contains(c.argv, stdPkg) && got != nil) {
			t.Errorf("%q: MK_ZDEC entries %q", c.line(), got)
		}
	}
	if s := g.rep.Steps[0]; s.Name != "test" || !slices.Contains(s.Env, want[0]) {
		t.Errorf("test step %q records env %q", s.Name, s.Env)
	}
	if got := zdecOf(g.env(spec{argv: []string{"go", "test", "-run=^$", "-fuzz=^FuzzCompress_Zstd$", stdPkg}})); !slices.Equal(got, want) {
		t.Errorf("fuzz of ./std: MK_ZDEC entries %q", got)
	}
	if got := zdecOf(g.env(spec{argv: []string{"go", "test", "-fuzz=^FuzzCheck$", "./internal/check"}})); got != nil {
		t.Errorf("fuzz of ./internal/check: MK_ZDEC entries %q", got)
	}
	if runtime.GOOS != "windows" {
		return // linuxStep runs on a Windows host only
	}
	*calls = nil
	g.linuxStep()
	saw := false
	for _, c := range *calls {
		got := zdecOf(c.env)
		std := slices.Contains(c.argv, stdPkg)
		saw = saw || std
		if std != slices.Equal(got, want) || (!std && got != nil) {
			t.Errorf("linux step %q: MK_ZDEC entries %q", c.line(), got)
		}
	}
	if !saw {
		t.Errorf("the linux step ran no ./std command: %q", *calls)
	}
}

// TestFakedGateBuildsNothing: a gate whose commands g.runCmd answers
// and that has no recorded zdec adds no step and no command, and its ./std
// run has no MK_ZDEC, whether or not the evidence and clang are present.
func TestFakedGateBuildsNothing(t *testing.T) {
	t.Setenv("MK_ZDEC", "")
	g, calls := fakeGate(t, "full", stepAnswer)
	g.only = []string{"./std"}
	g.testStep()
	if len(*calls) != 1 || len(g.rep.Steps) != 1 || g.rep.Steps[0].Name != "test" {
		t.Fatalf("calls %q, steps %d", *calls, len(g.rep.Steps))
	}
	if got := zdecOf((*calls)[0].env); got != nil {
		t.Errorf("./std run: MK_ZDEC entries %q", got)
	}
	zdecs.Lock()
	_, ok := zdecs.m[g]
	zdecs.Unlock()
	if ok {
		t.Errorf("a faked gate recorded a zdec")
	}
}

// realGate is a gate that runs its commands, with root and work given and
// work's directories made as setup makes them.
func realGate(t *testing.T, root, work string) *gate {
	t.Helper()
	for _, d := range []string{"tmp", "gocache", "logs"} {
		if err := os.MkdirAll(filepath.Join(work, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &gate{root: root, work: work, rep: &Report{}, skip: map[string]bool{}}
}

// TestStepSkipsAndFails: without the evidence files the step skips
// and says which file is absent; with files whose SHA-256 is not the pin
// it fails and names both hashes.
func TestStepSkipsAndFails(t *testing.T) {
	ev := t.TempDir()
	t.Setenv("MK_EVIDENCE", ev)
	g := realGate(t, t.TempDir(), t.TempDir())
	if p := g.zdecStep(); p != "" {
		t.Fatalf("zdec without evidence: %q", p)
	}
	s := g.rep.Steps[0]
	if s.Name != zdecStepName || s.Status != "skip" || !strings.Contains(s.Reason, "zstd-1.5.7.tar.gz is absent") {
		t.Errorf("no evidence: %s %q", s.Status, s.Reason)
	}
	for _, rel := range []string{zstdTarRel, zdecSrcRel} {
		p := filepath.Join(ev, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("not the pinned file\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g = realGate(t, t.TempDir(), t.TempDir())
	if p := g.zdecStep(); p != "" {
		t.Fatalf("zdec from unpinned files: %q", p)
	}
	s = g.rep.Steps[0]
	if s.Status != "fail" || !strings.Contains(s.Reason, "not the pinned "+zstdTarSHA) {
		t.Errorf("wrong pin: %s %q", s.Status, s.Reason)
	}
}

// forget drops g's zdec at the test's end.
func forget(t *testing.T, g *gate) {
	t.Cleanup(func() {
		zdecs.Lock()
		delete(zdecs.m, g)
		zdecs.Unlock()
	})
}

// TestEnvRunsTheStepOnce: in a gate that runs its commands, zdecFor
// before a ./std run and then every ./std command's environment go through
// one zdec step; here it skips (no evidence), so none has MK_ZDEC, and
// later commands add no second step.
func TestEnvRunsTheStepOnce(t *testing.T) {
	t.Setenv("MK_EVIDENCE", t.TempDir())
	t.Setenv("MK_ZDEC", `C:\stale\zdec.exe`)
	g := realGate(t, t.TempDir(), t.TempDir())
	forget(t, g)
	g.zdecFor([]goTestRun{{pkgs: []string{"./internal/check"}}})
	if len(g.rep.Steps) != 0 {
		t.Fatalf("a run without ./std ran the zdec step: %+v", g.rep.Steps[0])
	}
	g.zdecFor([]goTestRun{{pkgs: []string{"./internal/check"}}, {pkgs: []string{stdPkg}}})
	for i := 0; i < 2; i++ {
		if got := zdecOf(g.env(spec{argv: []string{"go", "test", stdPkg}})); got != nil {
			t.Errorf("./std command %d: MK_ZDEC entries %q", i, got)
		}
	}
	if len(g.rep.Steps) != 1 || g.rep.Steps[0].Name != zdecStepName || g.rep.Steps[0].Status != "skip" {
		t.Errorf("steps: %+v", g.rep.Steps)
	}
}

// TestMsvcEnv: INCLUDE and LIB from the environment win; else the
// newest MSVC tools and SDK that hold the files zdec needs, an incomplete
// newer one passed over; none gives the reason.
func TestMsvcEnv(t *testing.T) {
	none := func(string) string { return "" }
	set := func(k string) string { return map[string]string{"INCLUDE": "i", "LIB": "l"}[k] }
	if env, why := msvcEnv(set, nil); env != nil || why != "" {
		t.Errorf("INCLUDE and LIB set: %q %q", env, why)
	}
	base := t.TempDir()
	if env, why := msvcEnv(none, []string{base}); env != nil || !strings.Contains(why, "no MSVC tools") {
		t.Errorf("nothing installed: %q %q", env, why)
	}
	touch := func(parts ...string) {
		p := filepath.Join(append([]string{base}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vs := filepath.Join("Microsoft Visual Studio", "18", "Community", "VC", "Tools", "MSVC")
	for _, v := range []string{"14.9.1", "14.51.36231"} {
		touch(vs, v, "include", "vcruntime.h")
		touch(vs, v, "lib", "x64", "libcmt.lib")
	}
	touch(vs, "14.60.1", "include", "vcruntime.h") // no libcmt.lib
	kit := filepath.Join("Windows Kits", "10")
	for _, v := range []string{"10.0.22621.0", "10.0.26100.0"} {
		touch(kit, "Include", v, "ucrt", "stdio.h")
		touch(kit, "Lib", v, "ucrt", "x64", "libucrt.lib")
		touch(kit, "Lib", v, "um", "x64", "kernel32.lib")
	}
	touch(kit, "Include", "10.0.30000.0", "ucrt", "stdio.h") // no Lib
	env, why := msvcEnv(none, []string{filepath.Join(base, "absent"), base})
	vc := filepath.Join(base, vs, "14.51.36231")
	inc := filepath.Join(base, kit, "Include", "10.0.26100.0")
	lib := filepath.Join(base, kit, "Lib", "10.0.26100.0")
	want := []string{
		"INCLUDE=" + strings.Join([]string{filepath.Join(vc, "include"), filepath.Join(inc, "ucrt"), filepath.Join(inc, "um"), filepath.Join(inc, "shared")}, ";"),
		"LIB=" + strings.Join([]string{filepath.Join(vc, "lib", "x64"), filepath.Join(lib, "ucrt", "x64"), filepath.Join(lib, "um", "x64")}, ";"),
	}
	if why != "" || !slices.Equal(env, want) {
		t.Errorf("msvcEnv:\n got %q %q\nwant %q", env, why, want)
	}
}

// TestFindClangOrder: MK_LLVM's directory comes before the default
// directories and PATH, and one without clang falls through to them, as
// internal/toolchain's Discover does.
func TestFindClangOrder(t *testing.T) {
	d, p := t.TempDir(), t.TempDir()
	pathClang := filepath.Join(p, "clang"+exeSuffix())
	if err := os.WriteFile(pathClang, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Without clang in MK_LLVM: the default directory's when the host has
	// one, else PATH's.
	c, why := findClang(d, p)
	if why != "" || c == "" || strings.HasPrefix(c, d) {
		t.Errorf("MK_LLVM without clang: %q %q", c, why)
	}
	clang := filepath.Join(d, "clang"+exeSuffix())
	if err := os.WriteFile(clang, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if c, why := findClang(d, p); c != clang || why != "" {
		t.Errorf("MK_LLVM with clang: %q %q", c, why)
	}
}

// TestZstdWanted: the unpacked files are the two public headers and
// the files directly in common/ and decompress/.
func TestZstdWanted(t *testing.T) {
	for rel, want := range map[string]bool{
		"zstd.h": true, "zstd_errors.h": true, "common/xxhash.c": true, "decompress/zstd_decompress.c": true,
		"zdict.h": false, "compress/zstd_compress.c": false, "legacy/zstd_v01.c": false,
		"common/": false, "common/sub/x.c": false, "dll/example/x": false,
	} {
		if got := zstdWanted(rel); got != want {
			t.Errorf("zstdWanted(%q) = %v", rel, got)
		}
	}
}

// TestRealBuild builds zdec from the evidence directory (MK_EVIDENCE,
// or Mk-evidence beside the repository) with the host's clang: the step
// passes, zdec judges std's golden frames, whole and cut, and a second gate
// with the same work directory reuses the file untouched. It skips, saying
// why, where the step would skip.
func TestRealBuild(t *testing.T) {
	t.Setenv("MK_ZDEC", "")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	g := realGate(t, root, work)
	exe := g.zdecStep()
	s := g.rep.Steps[0]
	if s.Status == "skip" {
		t.Skipf("the zdec step skipped: %s", s.Reason)
	}
	if s.Status != "pass" || exe == "" {
		t.Fatalf("zdec step: %s %q; output %q", s.Status, s.Reason, s.Output)
	}
	if !strings.HasPrefix(exe, filepath.Join(work, "zdec")+string(os.PathSeparator)) {
		t.Errorf("zdec at %s, not under the work directory", exe)
	}
	frame, err := os.ReadFile(filepath.Join(root, "std", "testdata", "zstd", "real-l19.zst"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   []byte
		want string
	}{
		{"real-l19.zst", frame, "ok"},
		{"real-l19.zst twice", append(slices.Clone(frame), frame...), "ok"},
		{"real-l19.zst cut", frame[:len(frame)-1], "truncated"},
		{"empty input", nil, "truncated"},
	} {
		cmd := exec.Command(exe)
		cmd.Stdin = bytes.NewReader(c.in)
		out, err := cmd.Output()
		if f := strings.Fields(string(out)); err != nil || len(f) == 0 || f[0] != c.want {
			t.Errorf("%s: %q %v, want %s", c.name, out, err, c.want)
		}
	}
	before, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	g2 := realGate(t, root, work)
	if again := g2.zdecStep(); again != exe {
		t.Fatalf("second build: %q, want %q (%s %q)", again, exe, g2.rep.Steps[0].Status, g2.rep.Steps[0].Reason)
	}
	after, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || g2.rep.Steps[0].Command != "" {
		t.Errorf("the second gate rebuilt zdec: %v then %v, command %q", before.ModTime(), after.ModTime(), g2.rep.Steps[0].Command)
	}

	// The gate's own path: zdecFor before a ./std run, then the ./std
	// commands' environment, one step in all.
	g3 := realGate(t, root, work)
	forget(t, g3)
	g3.zdecFor([]goTestRun{{pkgs: []string{stdPkg}}})
	for i := 0; i < 2; i++ {
		if got := zdecOf(g3.env(spec{argv: []string{"go", "test", stdPkg}})); !slices.Equal(got, []string{"MK_ZDEC=" + exe}) {
			t.Errorf("./std command %d: MK_ZDEC entries %q", i, got)
		}
	}
	if len(g3.rep.Steps) != 1 || g3.rep.Steps[0].Status != "pass" {
		t.Errorf("third gate's steps: %+v", g3.rep.Steps)
	}

	// The reuse path still self-checks: a wrong zdec at the key's path
	// (this test binary, answering ok to everything) fails the step.
	fake, err := os.ReadFile(fakeZdec(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, fake, 0o755); err != nil {
		t.Fatal(err)
	}
	g4 := realGate(t, root, work)
	if p := g4.zdecStep(); p != "" || g4.rep.Steps[0].Status != "fail" || !strings.Contains(g4.rep.Steps[0].Reason, "cut frame") {
		t.Errorf("a wrong zdec at the reuse path: %q, %s %q", p, g4.rep.Steps[0].Status, g4.rep.Steps[0].Reason)
	}
}

// fakeZdecEnv makes this test binary a fake zdec (or a fake clang) when it
// is run with no -test. flag, as zdecSelfCheck and cHeaders run it; go test
// always passes -test. flags, so a caller's stray value never turns the
// package's own run into a fake. Its values:
//
//	ok, err, ...  that verdict for any input, exit 0
//	table         each zdecChecks frame's own verdict, "ok" for any other input
//	flip:<name>   as table, but the frame named <name> gets a wrong verdict
//	fail          a message on standard error, exit 1 (a clang that fails)
const fakeZdecEnv = "MK_GATE_FAKE_ZDEC"

func TestMain(m *testing.M) {
	if v := os.Getenv(fakeZdecEnv); v != "" && !slices.ContainsFunc(os.Args[1:], func(a string) bool { return strings.HasPrefix(a, "-test.") }) {
		in, _ := io.ReadAll(os.Stdin)
		fmt.Println(fakeVerdict(v, in))
		if v == "fail" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeVerdict is the fake's answer to in under mode v.
func fakeVerdict(v string, in []byte) string {
	if v == "fail" {
		fmt.Fprintln(os.Stderr, "fake: a failure that is not a missing header")
		return ""
	}
	flip, flipping := strings.CutPrefix(v, "flip:")
	if v != "table" && !flipping {
		return v
	}
	for _, c := range zdecChecks {
		if bytes.Equal(in, c.frame) {
			if flipping && c.name == flip {
				return map[string]string{"ok": "err", "err": "ok", "truncated": "ok"}[c.want]
			}
			return c.want
		}
	}
	return "ok"
}

// fakeZdec is this test binary, which answers verdict to every frame.
func fakeZdec(t *testing.T, verdict string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeZdecEnv, verdict)
	return exe
}

// TestSelfCheckRejectsAWrongZdec: the self-check is what rejects a
// zdec that answers "ok" to everything (no 8 MiB cap, no "truncated"); it
// fails at the first frame whose verdict differs, and with no clang.
func TestSelfCheckRejectsAWrongZdec(t *testing.T) {
	err := zdecSelfCheck(fakeZdec(t, "ok"))
	if err == nil || !strings.Contains(err.Error(), "cut frame") || !strings.Contains(err.Error(), "want truncated") {
		t.Errorf("a zdec that always answers ok: %v, want the cut frame's failure", err)
	}
	if err := zdecSelfCheck(fakeZdec(t, "err")); err == nil || !strings.Contains(err.Error(), "whole frame") {
		t.Errorf("a zdec that always answers err: %v, want the whole frame's failure", err)
	}
	if err := zdecSelfCheck(filepath.Join(t.TempDir(), "absent"+exeSuffix())); err == nil {
		t.Error("a missing zdec passed the self-check")
	}
}

// TestSelfCheckNamesEachFrame: every frame of zdecChecks is load
// bearing. A fake that answers each frame's own verdict
// passes; one that gets a single frame wrong fails, naming that frame,
// for each frame in turn (the 8 MiB window limit included). No clang.
func TestSelfCheckNamesEachFrame(t *testing.T) {
	// The frames the self-check must hold; a frame dropped from zdecChecks
	// fails here.
	var names []string
	for _, c := range zdecChecks {
		names = append(names, c.name+"="+c.want)
	}
	want := []string{"whole frame=ok", "cut frame=truncated", "reserved block type=err", "window of 16 MiB=err", "window of 8 MiB=ok"}
	if !slices.Equal(names, want) {
		t.Errorf("zdecChecks: %q, want %q", names, want)
	}
	if err := zdecSelfCheck(fakeZdec(t, "table")); err != nil {
		t.Fatalf("a zdec that answers every frame right: %v", err)
	}
	for _, c := range zdecChecks {
		err := zdecSelfCheck(fakeZdec(t, "flip:"+c.name))
		if err == nil || !strings.Contains(err.Error(), c.name+":") {
			t.Errorf("a zdec wrong on %q only: %v, want that frame's failure", c.name, err)
		}
	}
}

// TestNoZdecSpec: zdec's own build command (noZdec) gets no MK_ZDEC
// and starts no zdec step, even with ./std in its argv, so the build
// under zdecs' lock cannot take that lock again. It goes through plain
// and exec, as zdecStep runs clang, with zdecs' lock held (exec's record
// of the environment once reached zdecEnv on its own).
func TestNoZdecSpec(t *testing.T) {
	t.Setenv("MK_EVIDENCE", t.TempDir()) // a started step would skip at once
	g := realGate(t, t.TempDir(), t.TempDir())
	forget(t, g)
	if got := zdecOf(g.env(spec{argv: []string{"clang", stdPkg}, noZdec: true})); got != nil {
		t.Errorf("noZdec command: MK_ZDEC entries %q", got)
	}
	s := g.newStep("probe")
	// The fake zdec with ./std as its one argument exits 0 at once.
	argv := []string{fakeZdec(t, "ok"), stdPkg}
	done := make(chan int, 1)
	zdecs.Lock()
	go func() {
		_, code := g.plain(s, spec{argv: argv, noZdec: true})
		done <- code
	}()
	select {
	case code := <-done:
		zdecs.Unlock()
		if code != 0 {
			t.Errorf("the noZdec command exited %d", code)
		}
	case <-time.After(30 * time.Second):
		zdecs.Unlock()
		<-done
		t.Fatal("a noZdec command with ./std in its argv blocked on zdecs' lock for 30 s")
	}
	zdecs.Lock()
	_, built := zdecs.m[g]
	zdecs.Unlock()
	if built || len(g.rep.Steps) != 1 {
		t.Errorf("a noZdec command started the zdec step: %+v", g.rep.Steps)
	}
	if got := zdecOf(s.Env); got != nil {
		t.Errorf("the step's recorded environment has MK_ZDEC entries %q", got)
	}
}

// TestCHeaders: off Windows the step skips when clang finds no C
// library headers and fails when clang fails otherwise.
// Run here with this host's clang: on Windows, with INCLUDE and LIB
// cleared it finds no stdio.h (a skip), with msvcEnv's it finds them;
// and a "clang" that fails for another reason is a failure, not a skip.
func TestCHeaders(t *testing.T) {
	if skip, fail := cHeaders(fakeZdec(t, "fail")); skip != "" || !strings.Contains(fail, "fake: a failure") {
		t.Errorf("a clang that fails without a missing header: skip %q, fail %q; want a failure", skip, fail)
	}
	clang, why := findClang(os.Getenv("MK_LLVM"), os.Getenv("PATH"))
	if clang == "" {
		t.Logf("no clang (%s): only the fake was checked", why)
		return
	}
	if runtime.GOOS != "windows" {
		if skip, fail := cHeaders(clang); skip != "" || fail != "" {
			t.Logf("this host's clang: skip %q, fail %q", skip, fail)
		}
		return
	}
	cEnv, why := msvcEnv(func(string) string { return "" }, msvcBases())
	t.Setenv("INCLUDE", "")
	t.Setenv("LIB", "")
	if skip, fail := cHeaders(clang); !strings.Contains(skip, "finds no C library headers") || fail != "" {
		t.Errorf("clang with no INCLUDE: skip %q, fail %q; want a skip", skip, fail)
	}
	if why != "" {
		t.Logf("no MSVC environment (%s): the found-headers case was not checked", why)
		return
	}
	for _, kv := range cEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			t.Setenv(k, v)
		}
	}
	if skip, fail := cHeaders(clang); skip != "" || fail != "" {
		t.Errorf("clang with msvcEnv's INCLUDE and LIB: skip %q, fail %q; want neither", skip, fail)
	}
}

// TestKeyHashesTheCallersCEnv: on Windows, when the caller set INCLUDE
// and LIB (msvcEnv's nil), the key changes with them; elsewhere, and with
// msvcEnv's own environment, the key hashes that environment as given.
func TestKeyHashesTheCallersCEnv(t *testing.T) {
	env := func(inc, lib string) func(string) string {
		return func(k string) string {
			return map[string]string{"INCLUDE": inc, "LIB": lib}[k]
		}
	}
	a := zdecKey("clang", "clang version 21", zdecKeyEnv(env(`C:\a`, `C:\lib`), nil))
	b := zdecKey("clang", "clang version 21", zdecKeyEnv(env(`C:\b`, `C:\lib`), nil))
	if (a == b) != (runtime.GOOS != "windows") {
		t.Errorf("keys for two caller INCLUDEs: %s and %s (GOOS %s)", a, b, runtime.GOOS)
	}
	// LIB alone changes the key too.
	c := zdecKey("clang", "clang version 21", zdecKeyEnv(env(`C:\a`, `C:\lib2`), nil))
	if (a == c) != (runtime.GOOS != "windows") {
		t.Errorf("keys for two caller LIBs: %s and %s (GOOS %s)", a, c, runtime.GOOS)
	}
	own := []string{"INCLUDE=x", "LIB=y"}
	if got := zdecKeyEnv(env(`C:\a`, `C:\lib`), own); !slices.Equal(got, own) {
		t.Errorf("msvcEnv's environment: %q", got)
	}
}

// TestClangDefaults: findClang's Linux directories are
// internal/toolchain's DefaultDirs, which tools/gate may not import: the
// llvm-N list from MaxTestedMajor down to MinMajor, read from version.go.
func TestClangDefaults(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "toolchain", "version.go"))
	if err != nil {
		t.Fatal(err)
	}
	num := func(name string) int {
		for _, line := range strings.Split(string(src), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "const "+name+" = "); ok {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					t.Fatalf("%s: %q", name, line)
				}
				return n
			}
		}
		t.Fatalf("no const %s in internal/toolchain/version.go", name)
		return 0
	}
	var want []string
	for n := num("MaxTestedMajor"); n >= num("MinMajor"); n-- {
		want = append(want, fmt.Sprintf("/usr/lib/llvm-%d/bin", n))
	}
	want = append(want, "/usr/local/bin", "/usr/bin")
	if got := clangDefaults("linux"); !slices.Equal(got, want) {
		t.Errorf("clangDefaults(linux) = %q, want %q (internal/toolchain's DefaultDirs)", got, want)
	}
}
