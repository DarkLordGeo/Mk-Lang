// Why: mk check of 1 MLOC has a 2 GiB memory budget. Dropping each stage's
// output (release.go) bounds the live heap, but Go's collector lets the
// heap grow to twice the live set (GOGC=100) before it collects, so a
// 1 GiB live set is a 2 GiB process.
//
// What: the check's soft memory limit (checkMemLimit, memGiveUp,
// memBudget) with enterMemBudget and watchMemBudget.
//
// How: while a check runs, runtime/debug.SetMemoryLimit makes the collector
// run earlier, as long as the live set leaves it room; a program whose live
// set outgrows the budget gets the plain GOGC pacing back, so it is never
// slowed by a collector fighting a limit it cannot meet. The limit is
// counted across the sessions of one process.

package driver

import (
	"math"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"time"

	"mk/internal/source"
)

// checkMemLimit is the soft limit while a check runs: the 2 GiB budget less
// what is not Go heap in the working set (the executable's image, the
// stacks, the runtime's own metadata, which the limit partly counts).
const checkMemLimit = 1792 << 20

// memGiveUp is the live heap past which the limit is lifted: with less than
// a quarter of the limit free, the collector would run almost continuously.
const memGiveUp = checkMemLimit / 4 * 3

var memBudget struct {
	mu    sync.Mutex
	users int
	old   int64
	stop  chan struct{}
	done  chan struct{}
}

// enterMemBudget sets the check's soft memory limit for the process, unless
// GOMEMLIMIT is set (the user's choice wins) or another session of the
// process holds it already; the returned func undoes it when the last
// session leaves. Sessions run concurrently in one process (tests, mk lsp),
// so the limit is counted, not saved per session.
func enterMemBudget() func() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return func() {}
	}
	mb := &memBudget
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.users++
	if mb.users == 1 {
		mb.old = debug.SetMemoryLimit(checkMemLimit)
		mb.stop, mb.done = make(chan struct{}), make(chan struct{})
		stop, done := mb.stop, mb.done
		go func() {
			defer close(done)
			source.Guard("memory budget", source.Span{}, func() { watchMemBudget(stop) })
		}()
	}
	return func() {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		mb.users--
		if mb.users == 0 {
			close(mb.stop)
			<-mb.done
			debug.SetMemoryLimit(mb.old)
		}
	}
}

// watchMemBudget lifts the limit once the live heap passes memGiveUp, and
// keeps it lifted until stop.
func watchMemBudget(stop chan struct{}) {
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		metrics.Read(s)
		if s[0].Value.Kind() == metrics.KindUint64 && s[0].Value.Uint64() > memGiveUp {
			debug.SetMemoryLimit(math.MaxInt64)
			<-stop
			return
		}
	}
}
