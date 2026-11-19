// Why: extern calls on carriers' system stacks at sixteen carriers need a
// measured pass rate, not one stress seed's luck.
// What: TestRwRt_SixteenCarriers, std/task's RT-F21-SystemStackEveryCarrier
// block at MK_CARRIERS=16, opt-in with RWRT_F21_RUNS (default 45 runs).
// How: runs one release executable that many times with a fresh jitter seed
// each, on a quiet machine (under `go run ./tools/gate lock -- ...`).

package std

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRwRt_SixteenCarriers(t *testing.T) {
	rtsNative(t)
	env := os.Getenv("RWRT_F21_RUNS")
	if env == "" {
		t.Skip("set RWRT_F21_RUNS=<n> to run RT-F21-SystemStackEveryCarrier n times at 16 carriers")
	}
	runs := 45
	if _, err := fmt.Sscan(env, &runs); err != nil || runs <= 0 {
		t.Fatalf("RWRT_F21_RUNS=%q: want a positive count", env)
	}
	carriers := 16
	if s := os.Getenv("RWRT_F21_CARRIERS"); s != "" {
		fmt.Sscan(s, &carriers)
	}
	exe, blocks := rtsBuild(t, "release")
	var b *rtsBlock
	for i := range blocks {
		if strings.HasPrefix(blocks[i].name, "RT-F21-SystemStackEveryCarrier") {
			b = &blocks[i]
		}
	}
	if b == nil {
		t.Fatal("no RT-F21-SystemStackEveryCarrier block")
	}
	failed := 0
	for seed := 1; seed <= runs; seed++ {
		r := rtsRun(t, exe, b.index, 120*time.Second, append(rtsEnv(*b), fmt.Sprintf("MK_CARRIERS=%d", carriers),
			fmt.Sprintf("MK_TEST_SEED=%d", seed))...)
		notes := strings.Join(strings.Fields(r.stderr), " ")
		if len(notes) > 400 {
			notes = notes[:400]
		}
		if r.timeout || r.code != 0 {
			failed++
			t.Errorf("seed %d: exit %d (timeout %v) after %v: %s", seed, r.code, r.timeout, r.took, notes)
			continue
		}
		t.Logf("seed %d: ok in %v: %s", seed, r.took, notes)
	}
	t.Logf("%d of %d runs passed at %d carriers", runs-failed, runs, carriers)
}
