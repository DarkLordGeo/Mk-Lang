// Why: --timings shows where a run spends its time, without mixing timing
// lines into the diagnostic stream that tools parse.
//
// What: StageTime, FuncTime, pipeline.timed, the slow-function record
// (noteSlow, slowFuncs) and Result.WriteTimings.
//
// How: after the report, one line per stage of the run, then one line per
// function whose check plus borrow time exceeded 10 ms, sorted by key, are
// written to Config.Timings (stderr).

package driver

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"mk/internal/types"
)

// StageTime is the wall time of one stage of a run, in the order the stages
// ran. It is never part of --json or of a cache key.
type StageTime struct {
	Stage string
	Took  time.Duration
}

// FuncTime is one function's check plus borrow time (stages 5 and 7 on its
// body; lowering is not counted), for --timings. Never part of --json or of
// a cache key.
type FuncTime struct {
	Key  string // the item key
	Took time.Duration
}

// timed runs f and records its wall time under name, when timings are on.
func (p *pipeline) timed(name string, f func()) {
	if p.w.cfg.Timings == nil {
		f()
		return
	}
	start := time.Now()
	f()
	p.times = append(p.times, StageTime{Stage: name, Took: time.Since(start)})
}

// slowThreshold is 10 ms, or TestHooks.SlowFunc.
func (w *world) slowThreshold() time.Duration {
	if w.cfg.TestHooks.SlowFunc != 0 {
		return w.cfg.TestHooks.SlowFunc
	}
	return 10 * time.Millisecond
}

// clock is time.Now with --timings, else the zero time, so a run without
// timings reads no clock per body.
func (w *world) clock() time.Time {
	if w.cfg.Timings == nil {
		return time.Time{}
	}
	return time.Now()
}

// since is the time elapsed from a clock reading; zero without --timings.
func (w *world) since(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}

// noteSlow records it when its check plus borrow time passed the threshold.
// Safe for concurrent use: the body workers call it.
func (w *world) noteSlow(it *types.Item, took time.Duration) {
	if w.cfg.Timings == nil || took <= w.slowThreshold() {
		return
	}
	w.slowMu.Lock()
	w.slow = append(w.slow, FuncTime{Key: it.Key.String(), Took: took})
	w.slowMu.Unlock()
}

// slowFuncs is the recorded slow functions sorted by key, so the output
// never depends on completion order.
func (w *world) slowFuncs() []FuncTime {
	w.slowMu.Lock()
	defer w.slowMu.Unlock()
	out := slices.Clone(w.slow)
	slices.SortFunc(out, func(a, b FuncTime) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// WriteTimings writes "stage <name> <ms>ms" per stage in run order, then
// "func <key> <ms>ms" per function over the threshold, by key, then
// "pgo stale <symbol>" per function of a --pgo build without usable profile
// data, sorted, to w. A nil w writes nothing.
func (r Result) WriteTimings(w io.Writer) error {
	if w == nil {
		return nil
	}
	for _, t := range r.Timings {
		if _, err := fmt.Fprintf(w, "stage %s %s\n", t.Stage, millis(t.Took)); err != nil {
			return err
		}
	}
	for _, f := range r.SlowFuncs {
		if _, err := fmt.Fprintf(w, "func %s %s\n", f.Key, millis(f.Took)); err != nil {
			return err
		}
	}
	for _, s := range r.PGOStale {
		if _, err := fmt.Fprintf(w, "pgo stale %s\n", s); err != nil {
			return err
		}
	}
	return nil
}

// millis renders d as milliseconds with one decimal, "12.3ms".
func millis(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}
