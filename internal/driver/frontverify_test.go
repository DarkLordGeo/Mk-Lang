// Why: MK_CACHE_VERIFY recomputes cached entries to catch a stale cache; it
// must report a planted stale entry once and repair it.
// What: TestCacheVerifyShadowModeInterface.
// How: corrupts one byte of a stored interface record with its frame intact
// and checks the E0000 "cache verify" report, the cold output and the repair.

package driver

import (
	"bytes"
	"strings"
	"testing"

	"mk/internal/cache"
	"mk/internal/diag"
	"mk/internal/iface"
	"mk/internal/types"
)

// TestCacheVerifyShadowModeInterface: with MK_CACHE_VERIFY=1 a clean module cache
// verifies with no diagnostic and prints the cold --json; a planted stale
// interface entry (one byte of the record changed, the frame intact, so
// the store still serves it) gives exactly one E0000 where "cache verify"
// naming it, the rest of the report equals the cold one, and the entry is
// replaced, so a plain warm check after it loads lib from the cache again
// and prints the cold bytes.
func TestCacheVerifyShadowModeInterface(t *testing.T) {
	cfg := Config{Root: writeTree(t, incPkg("x + 1")), CacheDir: t.TempDir(), Target: types.X86_64Windows}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cold := s.Check()
	if len(cold.Report.Diags) > 0 || len(cold.Trace.NotStored) > 0 {
		t.Fatalf("cold: %v, not stored %q", codes(cold), cold.Trace.NotStored)
	}
	verify := cfg
	verify.CacheVerify = true
	clean := frontCheck(t, verify)
	if !bytes.Equal(jsonOf(t, clean), jsonOf(t, cold)) || len(clean.Trace.ModulesFromCache) > 0 {
		t.Fatalf("verify over a clean cache: %s, from cache %q", jsonOf(t, clean), clean.Trace.ModulesFromCache)
	}
	// Plant: lib's interface entry, one byte of the record flipped.
	lib := s.pkgB.byName["lib"]
	st, err := cache.Open(cfg.CacheDir, cache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mraw, ok, _ := st.Get(cache.KindManifest, lib.prefix)
	if !ok {
		t.Fatal("no manifest for lib")
	}
	man, err := iface.DecodeManifest(mraw)
	if err != nil || len(man.Candidates) == 0 {
		t.Fatalf("manifest: %v", err)
	}
	entry := man.Candidates[0].Entry
	raw, ok, _ := st.Get(cache.KindIface, entry)
	if !ok {
		t.Fatal("no interface entry")
	}
	keys, rec, err := unframeIface(raw)
	if err != nil {
		t.Fatal(err)
	}
	rec = bytes.Clone(rec)
	rec[len(rec)-1] ^= 0x40
	if err := st.RemoveFile(cache.KindIface, entry); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(cache.KindIface, entry, frameIface(keys, rec)); err != nil {
		t.Fatal(err)
	}
	stale := frontCheck(t, verify)
	var e0000 []string
	for _, d := range stale.Report.Diags {
		if d.Diag.Code == diag.E0000 {
			e0000 = append(e0000, fact(d.Diag, "where")+": "+fact(d.Diag, "what"))
		}
	}
	if len(e0000) != 1 || !strings.Contains(e0000[0], "cache verify") || !strings.Contains(e0000[0], entry.String()) ||
		len(stale.Report.Diags) != 1 || ExitStatus(stale.Report) != 3 {
		t.Fatalf("stale entry: %d diagnostics, E0000s %q, exit %d", len(stale.Report.Diags), e0000, ExitStatus(stale.Report))
	}
	warm := frontCheck(t, cfg)
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) || !strings.Contains(strings.Join(warm.Trace.ModulesFromCache, " "), "lib") {
		t.Errorf("after the verify run: %s, from cache %q", jsonOf(t, warm), warm.Trace.ModulesFromCache)
	}
}
