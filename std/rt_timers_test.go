// Why: the timer scan must work at carrier counts where a carrier's record
// lies past the committed 64 KiB units or past StealRounds' seen array.
// What: TestRt_ScanClampCarriers, RT8-Tm-ScanClamp (std/task/tests_timers.mk)
// at 49, 98 and 256 (MaxCarriers) carriers, through rt-sched_harness_test.go.
// How: 49 and 98 put the record past a unit boundary at SizeOf[Carrier] 2,672;
// other tests run the block at their own counts.

package std

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRt_ScanClampCarriers runs RT8-Tm-ScanClamp, release, at 49, 98 and
// 256 carriers, each under a 60 s watchdog.
func TestRt_ScanClampCarriers(t *testing.T) {
	rtsNative(t)
	exe, all := rtsBuild(t, "release")
	var found []rtsBlock
	for _, b := range all {
		if strings.HasPrefix(b.name, "RT8-Tm-ScanClamp ") {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d RT8-Tm-ScanClamp blocks, want 1", len(found))
	}
	b := found[0]
	for _, n := range []int{49, 98, 256} {
		n := n
		t.Run(fmt.Sprintf("carriers=%d", n), func(t *testing.T) {
			r := rtsRun(t, exe, b.index, 60*time.Second, append(rtsEnv(b), fmt.Sprintf("MK_CARRIERS=%d", n))...)
			rtsJudge(t, b, r, "")
		})
	}
}
