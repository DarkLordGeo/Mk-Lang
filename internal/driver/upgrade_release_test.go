// Why: mk upgrade replaces the compiler itself, so it must never downgrade by
// surprise, follow a redirect to plain http, or hide a half-done install.
// What: TestUpgradeNoDowngrade, TestUpgradeSelfContainedNeedsARelease,
// TestUpgradeRefusesHTTPRedirect and TestUpgradeLateFailureSaysWhatWasInstalled.
// How: serves releases from httptest; every root is under the test's
// temporary directory.

package driver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/release"
)

// TestUpgradeNoDowngrade: mk upgrade with no version refuses a
// release that is not newer than current, or than the running mk, and
// says to name the version; named, the older version is installed.
func TestUpgradeNoDowngrade(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.4.0", false)
	root := t.TempDir()
	if _, err := Upgrade(upgradeCfg(s, root, "0.4.0", priv)); err != nil {
		t.Fatal(err)
	}
	s.publish(t, priv, "0.3.0", true) // the newest address now serves an older release
	before := snapshot(t, root)
	_, err := Upgrade(upgradeCfg(s, root, "", priv))
	if err == nil || !strings.Contains(err.Error(), "not newer") || !strings.Contains(err.Error(), "mk upgrade 0.3.0") {
		t.Errorf("a downgrade with no version: %v", err)
	}
	if after := snapshot(t, root); after != before {
		t.Errorf("the refusal changed the root:\n%s\n%s", before, after)
	}
	// The same release as current is not newer either.
	s.publish(t, priv, "0.4.0", true)
	if _, err := Upgrade(upgradeCfg(s, root, "", priv)); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("the same release again: %v", err)
	}
	// Not newer than the running mk, with no current.
	cfg := upgradeCfg(s, t.TempDir(), "", priv)
	cfg.Running = "0.5.0"
	if _, err := Upgrade(cfg); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("older than the running mk: %v", err)
	}
	// Named, the older version installs.
	res, err := Upgrade(upgradeCfg(s, root, "0.3.0", priv))
	if err != nil || res.New != "0.3.0" || res.Old != "0.4.0" {
		t.Fatalf("a named older version: %+v %v", res, err)
	}
}

// TestUpgradeSelfContainedNeedsARelease: an mk that is not in an
// unpacked release (bin/mk beside RELEASE.txt or llvm/bin) is refused, and
// nothing is written beside it.
func TestUpgradeSelfContainedNeedsARelease(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.4.0", true)
	for _, dir := range []string{filepath.Join("a", "b", "tools"), filepath.Join("a", "plain", "bin")} {
		parent := t.TempDir()
		own := filepath.Join(parent, dir)
		os.MkdirAll(own, 0o755)
		exe := filepath.Join(own, mkExeName())
		os.WriteFile(exe, []byte("mk"), 0o755)
		before := snapshot(t, parent)
		_, err := Upgrade(UpgradeConfig{Base: s.srv.URL, Exe: exe, Keys: []release.Key{release.PublicOf(priv)},
			Getenv: func(string) string { return "" }, Running: "0.2.0"})
		if err == nil || !strings.Contains(err.Error(), "not in an unpacked release") {
			t.Errorf("%s: %v", dir, err)
		}
		if after := snapshot(t, parent); after != before {
			t.Errorf("%s: the refusal wrote:\n%s", dir, after)
		}
	}
}

// TestUpgradeRefusesHTTPRedirect: a redirect from the release
// address to plain http on another host is refused before any request to
// it, and nothing changes.
func TestUpgradeRefusesHTTPRedirect(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.3.0", false)
	root := t.TempDir()
	if _, err := Upgrade(upgradeCfg(s, root, "0.3.0", priv)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid"+r.URL.Path, http.StatusFound)
	}))
	defer redir.Close()
	cfg := upgradeCfg(s, root, "0.4.0", priv)
	cfg.Base = redir.URL
	if _, err := Upgrade(cfg); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("redirect to http: %v", err)
	}
	if after := snapshot(t, root); after != before {
		t.Error("the refusal changed the root")
	}
}

// TestUpgradeLateFailureSaysWhatWasInstalled: when the new version
// is unpacked but current cannot move, the result says where the new
// version is, and the message names it instead of "nothing was changed".
func TestUpgradeLateFailureSaysWhatWasInstalled(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.4.0", true)
	root := t.TempDir()
	// current is a plain directory holding a file: Use cannot move it.
	os.MkdirAll(filepath.Join(root, "current"), 0o755)
	os.WriteFile(filepath.Join(root, "current", "keep"), []byte("x"), 0o644)
	res, err := Upgrade(upgradeCfg(s, root, "0.4.0", priv))
	if err == nil {
		t.Fatal("Use over a plain directory succeeded")
	}
	if !res.Unpacked || res.Dir == "" {
		t.Errorf("result %+v does not say the version was unpacked", res)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "bin", mkExeName())); err != nil {
		t.Errorf("the unpacked version: %v", err)
	}
	msg := UpgradeFailure(res, err)
	if strings.Contains(msg, "nothing was changed") || !strings.Contains(msg, res.Dir) || !strings.Contains(msg, "0.4.0") {
		t.Errorf("message: %s", msg)
	}
	if msg := UpgradeFailure(UpgradeResult{}, err); !strings.Contains(msg, "nothing was changed") {
		t.Errorf("an early failure: %s", msg)
	}
}
