// Why: a std on disk (MK_STD) is part of the compiler, as the embedded std
// is through the build ID, so editing it must invalidate what read it.
// What: TestInvalidation_StdDirEditMissesImplicitReaders and
// TestImplicitRT_ScheduledAfterRT.
// How: checks a package against a std copy, edits the copy, and requires
// the modules that read it implicitly to miss; frontcache.go's stdContext
// holds the std's hash.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestInvalidation_StdDirEditMissesImplicitReaders: an edit of a std module
// the package does not import (std.rt's hooks are such modules: lowering
// calls them with no import) misses the package's module under the same
// MK_STD and cache. Before the fix the module hit, and its function
// results named std items by key that the edited std no longer had
// ("iface: entry refers to something that changed: item std.rt.TraceTry",
// reported by std-compress).
func TestInvalidation_StdDirEditMissesImplicitReaders(t *testing.T) {
	stdDir := fakeStd(t)
	root := writeTree(t, map[string]string{"a.mk": "export A(): int {\n    return 1\n}\n"})
	cfg := Config{Root: filepath.Join(root, "a.mk"), StdDir: stdDir, CacheDir: t.TempDir()}
	clean(t, check(t, cfg))
	r := check(t, cfg)
	clean(t, r)
	if !slices.Contains(r.Trace.ModulesFromCache, "main") {
		t.Fatalf("warm: main not from the cache: %v", r.Trace.ModulesFromCache)
	}
	// std.fs is not imported by main.
	if err := os.WriteFile(filepath.Join(stdDir, "fs", "fs.mk"), []byte("export ReadText(): int {\n    return 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = check(t, cfg)
	clean(t, r)
	if slices.Contains(r.Trace.ModulesFromCache, "main") {
		t.Errorf("after a std edit under the same MK_STD, main was a hit: %v", r.Trace.ModulesFromCache)
	}
	// And warm again after it.
	if r = check(t, cfg); !slices.Contains(r.Trace.ModulesFromCache, "main") {
		t.Errorf("rewarm: main not from the cache: %v", r.Trace.ModulesFromCache)
	}
}

// TestImplicitRT_ScheduledAfterRT: a std module outside std.rt's import
// closure (std.crypto imports std.hash and std.raw, not std.rt) starts its
// stages only after std.rt's are done, even when std.rt is slow, because
// lowering reads std.rt.Site and the trace hooks with no import
// (schedule.go implicitRT). Otherwise the two race: the ICE "facts of
// std.rt.Site asked before its module's impls are registered", or a
// module hit naming a std.rt.TraceTry not yet loaded.
func TestImplicitRT_ScheduledAfterRT(t *testing.T) {
	root := writeTree(t, map[string]string{"a.mk": "export A(): int {\n    return 1\n}\n"})
	var mu sync.Mutex
	var events []string
	hook := func(module string, st Stage, begin bool) {
		if module == "std.rt" && begin && st == StageSignatures {
			time.Sleep(300 * time.Millisecond)
		}
		mu.Lock()
		events = append(events, fmt.Sprintf("%s %v %v", module, st, begin))
		mu.Unlock()
	}
	r := check(t, Config{Root: filepath.Join(root, "a.mk"), NoCache: true, TestHooks: TestHooks{OnStage: hook}})
	clean(t, r)
	rtEnd, cryptoBegin := -1, -1
	for i, e := range events {
		if strings.HasPrefix(e, "std.rt ") && strings.HasSuffix(e, " false") {
			rtEnd = i
		}
		if strings.HasPrefix(e, "std.crypto ") && strings.HasSuffix(e, " true") && cryptoBegin < 0 {
			cryptoBegin = i
		}
	}
	if rtEnd < 0 || cryptoBegin < 0 {
		t.Skipf("std has no std.rt or std.crypto stages: %d events", len(events))
	}
	if cryptoBegin < rtEnd {
		t.Errorf("std.crypto began (event %d) before std.rt ended (event %d)", cryptoBegin, rtEnd)
	}
}
