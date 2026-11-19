// Why: the false-rejection rate is reported with a Wilson interval, and both
// the arithmetic and the parallel sampling must be exact.
// What: TestSound_Wilson and TestSound_RejectSmall.
// How: fixed interval values, and a small sample run with 1 and 4 workers.

package corpus

import (
	"context"
	"fmt"
	"math"
	"testing"

	"mk/internal/driver"
)

func TestSound_Wilson(t *testing.T) {
	lo, hi := wilson(10, 100)
	if math.Abs(lo-0.0552) > 0.001 || math.Abs(hi-0.1744) > 0.001 {
		t.Fatalf("wilson(10, 100) = [%.4f, %.4f], want about [0.0552, 0.1744]", lo, hi)
	}
	if lo, hi := wilson(0, 0); lo != 0 || hi != 0 {
		t.Fatalf("wilson(0, 0) = [%v, %v]", lo, hi)
	}
}

// TestSound_RejectSmall samples 40 rejected programs, twice
// with 1 and 4 workers: the same counts.
func TestSound_RejectSmall(t *testing.T) {
	s := soundSess(t)
	var got []string
	for _, w := range []int{1, 4} {
		cfg := SoundConfig{Base: DefaultSoundBase, Reject: 40, Workers: w, Label: fmt.Sprintf("t7-%d", soundUnitN.Add(1))}
		_, t7, err := RunReject(context.Background(), cfg, NewSessionCompiler(driver.Config{}, s))
		if err != nil {
			t.Fatal(err)
		}
		if t7.Sampled != 40 {
			t.Fatalf("workers %d: sampled %d, want 40", w, t7.Sampled)
		}
		got = append(got, fmt.Sprintf("%d %d %d %d %v %+v body %+v boundary %+v twins %+v adjusted %+v", t7.Generated, t7.MoveOnly, t7.Accepted, t7.Defects, t7.Skipped,
			t7.Overall, t7.Body, t7.Boundary, t7.Twins, t7.Adjusted))
		if b, n := t7.Body.Runnable+t7.Boundary.Runnable, t7.Overall.Runnable; b != n {
			t.Errorf("workers %d: body %d + boundary %d runnable, want the overall %d", w, t7.Body.Runnable, t7.Boundary.Runnable, n)
		}
		if tw := t7.Twins; tw.Accepted+tw.Rejected+tw.Move+tw.Defects+tw.ICE != tw.Dropped {
			t.Errorf("workers %d: twins %+v do not add up", w, tw)
		}
		t.Logf("workers %d: %s", w, got[len(got)-1])
	}
	if got[0] != got[1] {
		t.Fatalf("1 and 4 workers differ:\n%s\n%s", got[0], got[1])
	}
}
