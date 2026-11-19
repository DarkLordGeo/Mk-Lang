// Why: profile-guided optimization spans the runtime (writing profiles),
// mk profile merge and codegen (branch weights); only a real run shows the
// pieces fit.
// What: pgoLoopProgram and TestPGOEndToEnd, on x86_64-windows natively and
// x86_64-linux through the target's runner.
// How: the instrumented program writes <name>-<pid>.mkprof into
// MK_PROFILE_DIR at exit (std/rt/profile.mk); two runs are merged and the
// --pgo build's IR must carry the merged entry count and branch weights.
// MK_PROFILE_DIR is given as the runner's path and passed through WSLENV.

package driver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/pgo"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// pgoLoopProgram calls Pick(i) for i in 0..10: Pick's entry runs 10 times,
// the x > 3 edge 6 times and the other 4. The exit status is 0.
const pgoLoopProgram = "Pick(x: int): int {\n    if x > 3 {\n        return 1\n    }\n    return 2\n}\n\n" +
	"export Main(sys: &System): int {\n    var s := 0\n    for i in 0..10 {\n        s += Pick(i)\n    }\n    return s - 14\n}\n"

// pgoBuildExe is one release build of root with mode and file into out; it
// returns the result and every pack's .ll text by pack name.
func pgoBuildExe(t *testing.T, target types.Target, root string, mode PGOMode, file, out string, timings bool) (Result, map[string]string) {
	t.Helper()
	var mu sync.Mutex
	lls := map[string]string{}
	cfg := Config{Root: root, CacheDir: t.TempDir(), Target: target, Profile: types.Release, PGO: mode, PGOFile: file,
		Tools: needTools(t), TestHooks: TestHooks{OnLL: func(name, ll string) {
			mu.Lock()
			lls[name] = ll
			mu.Unlock()
		}}}
	if timings {
		cfg.Timings = &bytes.Buffer{}
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		skipKnownBlockers(t, r)
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	return r, lls
}

// runWithProfileDir runs exe through the target's runner prefix (none for
// the host) with MK_PROFILE_DIR=dir, as the runner sees the path; through
// wsl.exe the variable is passed by WSLENV. It wants status 0 and no
// output.
func runWithProfileDir(t *testing.T, prefix []string, exe, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	argv := append(append([]string(nil), prefix...), toolchain.HostPathFor(prefix, exe))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "MK_PROFILE_DIR="+toolchain.HostPathFor(prefix, dir))
	if len(prefix) > 0 {
		wslenv := "MK_PROFILE_DIR"
		if old := os.Getenv("WSLENV"); old != "" {
			wslenv = old + ":" + wslenv
		}
		cmd.Env = append(cmd.Env, "WSLENV="+wslenv)
	}
	var o bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &o
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, o.String())
	}
	if o.Len() != 0 {
		t.Fatalf("%s printed %q", exe, o.String())
	}
}

// readProfile reads a .mkprof and returns Pick's record.
func readProfile(t *testing.T, path string) pgo.Record {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pgo.Read(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, r := range p.Records {
		if r.Key == "mk.main.Pick" {
			return r
		}
	}
	t.Fatalf("%s has no record of mk.main.Pick (%d records)", path, len(p.Records))
	return pgo.Record{}
}

// pickDef is the text of Pick's definition in the packs, through its
// closing brace, plus the module's metadata lines.
func pickDef(t *testing.T, lls map[string]string) (string, string) {
	t.Helper()
	for _, ll := range lls {
		for _, def := range strings.Split(ll, "\ndefine ")[1:] {
			if strings.HasPrefix(def[strings.Index(def, "@"):], "@mk.main.Pick(") {
				return def[:strings.Index(def, "\n}\n")], ll
			}
		}
	}
	t.Fatal("no definition of mk.main.Pick in the packs")
	return "", ""
}

// TestPGOEndToEnd: a --pgo-gen run, mk profile merge and a --pgo build
// work and change Pick's entry count and branch weights, on both targets.
func TestPGOEndToEnd(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			pgoEndToEnd(t, target, runner(t, target))
		})
	}
}

func pgoEndToEnd(t *testing.T, target types.Target, prefix []string) {
	dir := writeTree(t, map[string]string{"p.mk": pgoLoopProgram})
	root := filepath.Join(dir, "p.mk")
	exe := filepath.Join(t.TempDir(), "p"+exeSuffix(target))
	_, gen := pgoBuildExe(t, target, root, PGOGen, "", exe, false)
	sum, n := profileRecord(t, gen["support"], "mk.main.Pick")

	// Two runs write two profiles (distinct pids) into one directory.
	profDir := t.TempDir()
	runWithProfileDir(t, prefix, exe, profDir)
	runWithProfileDir(t, prefix, exe, profDir)
	files, err := filepath.Glob(filepath.Join(profDir, "p-*.mkprof"))
	if err != nil || len(files) != 2 {
		t.Fatalf("profiles written: %q (%v), want two p-<pid>.mkprof", files, err)
	}
	one := readProfile(t, files[0])
	if one.Checksum != sum || len(one.Counts) != n {
		t.Fatalf("the run's record of Pick: checksum %x, %d counters; the build's table: %x, %d", one.Checksum, len(one.Counts), sum, n)
	}
	if one.Counts[0] != 10 || one.Counts[1]+one.Counts[2] != 10 || one.Counts[1]*one.Counts[2] != 24 {
		t.Fatalf("one run's counts of Pick %v, want the entry 10 and the if's edges 6 and 4", one.Counts)
	}

	// mk profile merge sums them.
	merged := filepath.Join(t.TempDir(), "merged.mkprof")
	if _, err := MergeProfiles(merged, files); err != nil {
		t.Fatal(err)
	}
	m := readProfile(t, merged)
	for i := range m.Counts {
		if m.Counts[i] != 2*one.Counts[i] {
			t.Fatalf("merged counts %v, want twice %v", m.Counts, one.Counts)
		}
	}

	// The --pgo build carries them: entry count 20, branch weights from the
	// if's edge counts, and the program still runs.
	out := filepath.Join(t.TempDir(), "q"+exeSuffix(target))
	r, use := pgoBuildExe(t, target, root, PGOUse, merged, out, true)
	def, _ := pickDef(t, use)
	ll := strings.Join(mapValues(use), "\n")
	if !strings.Contains(ll, fmt.Sprintf(`!"function_entry_count", i64 %d}`, m.Counts[0])) {
		t.Errorf("no function_entry_count %d in the packs; Pick:\n%s", m.Counts[0], def)
	}
	ws := pgo.Weights(m.Counts[1:3])
	want := fmt.Sprintf(`!"branch_weights", i32 %d, i32 %d}`, ws[0], ws[1])
	alt := fmt.Sprintf(`!"branch_weights", i32 %d, i32 %d}`, ws[1], ws[0])
	if !strings.Contains(ll, want) && !strings.Contains(ll, alt) {
		t.Errorf("no branch_weights %v in the packs; Pick:\n%s", ws, def)
	}
	if !strings.Contains(def, "!prof") {
		t.Errorf("Pick carries no !prof:\n%s", def)
	}
	if len(r.PGOStale) != 0 {
		t.Errorf("PGOStale %q with a fresh profile", r.PGOStale)
	}
	runWithProfileDir(t, prefix, out, t.TempDir())

	// An edited Pick (another CFG) makes its record stale: no weights, and
	// --timings names it.
	edited := strings.Replace(pgoLoopProgram, "    return 2\n}", "    if x > 1 {\n        return 2\n    }\n    return 2\n}", 1)
	if err := os.WriteFile(root, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	var tim bytes.Buffer
	r, use = pgoBuildExe(t, target, root, PGOUse, merged, filepath.Join(t.TempDir(), "e"+exeSuffix(target)), true)
	if def, _ := pickDef(t, use); strings.Contains(def, "!prof") {
		t.Errorf("a stale record still gives Pick weights:\n%s", def)
	}
	if len(r.PGOStale) != 1 || r.PGOStale[0] != "mk.main.Pick" {
		t.Errorf("PGOStale %q, want [mk.main.Pick]", r.PGOStale)
	}
	if err := r.WriteTimings(&tim); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tim.String(), "pgo stale mk.main.Pick\n") {
		t.Errorf("--timings output has no stale line:\n%s", tim.String())
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
