// Why: mk test --sim SEED and --prop pass their settings to each test's
// child process through the environment, and a stray MK_SIM_SEED from the
// caller's own environment must not leak in.
// What: TestTestSimSeedReachesChild, TestPropNoContracts, TestChildEnv,
// TestTestEnvDropsOutsideSeed and TestTestSimSeedNotInherited.
// How: runs mk test with and without --sim and with a planted MK_SIM_SEED,
// and reads the child's environment, WSLENV and the report.

package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const simSeedSource = `test "sees the seed" (sys: &System) {
    Assert((sys.Env("MK_SIM_SEED") else { "" }) == "18446744073709551615")
}
`

// TestTestSimSeedReachesChild: --sim SEED reaches every child as
// MK_SIM_SEED, the report carries it in JSON and text, and without --sim
// the child does not see it.
func TestTestSimSeedReachesChild(t *testing.T) {
	t.Setenv(SimSeedEnv, "")
	dir := testPkg(t, map[string]string{"t.mk": simSeedSource})
	root := filepath.Join(dir, "t.mk")
	rep := mkTest(t, Config{Root: root}, TestOptions{Sim: 1<<64 - 1, SimSet: true})
	if got := results(rep); got != "main sees the seed pass" {
		t.Fatalf("with --sim: %s\n%+v", got, rep.Tests)
	}
	var b bytes.Buffer
	if err := rep.WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["sim_seed"]; !ok || !strings.Contains(b.String(), `"sim_seed":18446744073709551615`) {
		t.Errorf("json lacks the seed: %s", b.String())
	}
	b.Reset()
	_ = rep.WriteText(&b, false)
	if !strings.HasSuffix(b.String(), "1 passed, 0 failed (sim seed 18446744073709551615)\n") {
		t.Errorf("text: %q", b.String())
	}
	rep = mkTest(t, Config{Root: root}, TestOptions{})
	if got := results(rep); got != "main sees the seed panic" {
		t.Errorf("without --sim the child saw a seed: %s", got)
	}
	b.Reset()
	_ = rep.WriteJSON(&b)
	if strings.Contains(b.String(), "sim_seed") {
		t.Errorf("json without --sim carries a seed: %s", b.String())
	}
}

// TestPropNoContracts: --prop runs the property tester instead of the test
// children, so a file with a test block and no contract reports nothing and
// carries the generator's seed. Release is a usage error: contracts are off
// there.
func TestPropNoContracts(t *testing.T) {
	dir := testPkg(t, map[string]string{"t.mk": "test \"x\" {\n    Assert(false)\n}\n"})
	s, err := NewSession(Config{Root: filepath.Join(dir, "t.mk"), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := s.Test(TestOptions{Prop: true})
	if err != nil || rep == nil || len(rep.Tests) != 0 || rep.PropSeed == nil || *rep.PropSeed != PropDefaultSeed || rep.ExitStatus() != 0 {
		t.Fatalf("--prop: rep %+v err %v", rep, err)
	}
	s, err = NewSession(Config{Root: filepath.Join(dir, "t.mk"), CacheDir: t.TempDir(), Profile: ProfileRelease})
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err = s.Test(TestOptions{Prop: true})
	var ue *UsageError
	if rep != nil || !errors.As(err, &ue) || !strings.Contains(err.Error(), "release") {
		t.Fatalf("--prop in release: rep %v err %v", rep, err)
	}
}

// TestChildEnv: a set name replaces the inherited one (case-insensitively),
// and through a runner WSLENV lists it, after any inherited WSLENV.
func TestChildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "mk_sim_seed=1", "WSLENV=FOO/p"}
	got := childEnv(base, false, "MK_SIM_SEED=7")
	if want := []string{"PATH=/bin", "WSLENV=FOO/p", "MK_SIM_SEED=7"}; !slices.Equal(got, want) {
		t.Errorf("host: %q", got)
	}
	got = childEnv(base, true, "MK_SIM_SEED=7")
	if want := []string{"PATH=/bin", "MK_SIM_SEED=7", "WSLENV=FOO/p:MK_SIM_SEED/u"}; !slices.Equal(got, want) {
		t.Errorf("runner: %q", got)
	}
}

// TestTestEnvDropsOutsideSeed: without --sim the child's environment
// lacks MK_SIM_SEED even when mk's has one (any case), and through a runner
// WSLENV no longer lists it; with --sim the given seed replaces it.
func TestTestEnvDropsOutsideSeed(t *testing.T) {
	base := []string{"PATH=/bin", "mk_sim_seed=1", "WSLENV=FOO/p:MK_SIM_SEED/u"}
	if got, want := testEnv(base, false, TestOptions{}), []string{"PATH=/bin", "WSLENV=FOO/p:MK_SIM_SEED/u"}; !slices.Equal(got, want) {
		t.Errorf("host: %q", got)
	}
	if got, want := testEnv(base, true, TestOptions{}), []string{"PATH=/bin", "WSLENV=FOO/p"}; !slices.Equal(got, want) {
		t.Errorf("runner: %q", got)
	}
	if got, want := testEnv(base, false, TestOptions{Sim: 9, SimSet: true}), []string{"PATH=/bin", "WSLENV=FOO/p:MK_SIM_SEED/u", "MK_SIM_SEED=9"}; !slices.Equal(got, want) {
		t.Errorf("--sim: %q", got)
	}
}

// TestTestSimSeedNotInherited: a plain mk test whose own environment
// holds MK_SIM_SEED runs its child with no seed, as its report says.
func TestTestSimSeedNotInherited(t *testing.T) {
	t.Setenv(SimSeedEnv, "7")
	dir := testPkg(t, map[string]string{"t.mk": "test \"no seed\" (sys: &System) {\n    Assert((sys.Env(\"MK_SIM_SEED\") else { \"absent\" }) == \"absent\")\n}\n"})
	rep := mkTest(t, Config{Root: filepath.Join(dir, "t.mk")}, TestOptions{})
	if got := results(rep); got != "main no seed pass" || rep.SimSeed != nil {
		t.Errorf("the child saw mk's MK_SIM_SEED: %s, seed %v", got, rep.SimSeed)
	}
}
