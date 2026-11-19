// Why: no mk process a gate step starts may touch the per-user cache.
// What: TestEnvWith_MKCacheInWork and TestEnvWith_CorpusMKCacheOnce.
// How: build step environments with envWith and count the MK_CACHE entries:
// one, inside the work directory, and the corpus step's own only without one.

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvWith_MKCacheInWork(t *testing.T) {
	t.Setenv("MK_CACHE", filepath.Join(t.TempDir(), "outer"))
	work := t.TempDir()
	g := &gate{work: work}
	var got []string
	for _, kv := range g.envWith(spec{argv: []string{"go", "test", "./..."}}, nil) {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "MK_CACHE") {
			got = append(got, v)
		}
	}
	if want := filepath.Join(work, "mkcache"); len(got) != 1 || got[0] != want {
		t.Errorf("MK_CACHE entries %q, want one, %s", got, want)
	}
}

// The corpus step's environment holds MK_CACHE once: envWith
// sets it when the gate has a work directory, and the step's own entry
// only when it has none.
func TestEnvWith_CorpusMKCacheOnce(t *testing.T) {
	for _, work := range []string{t.TempDir(), ""} {
		g := &gate{work: work}
		n := 0
		for _, kv := range g.envWith(spec{argv: []string{"go", "run", "./cmd/mk", "corpus"}, set: g.corpusSet()}, nil) {
			if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, "MK_CACHE") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("work %q: %d MK_CACHE entries, want 1", work, n)
		}
	}
}
