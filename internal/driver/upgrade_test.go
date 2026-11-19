// Why: mk upgrade downloads, verifies and swaps whole toolchains; a bad
// signature or an interrupted unpack must leave the current one working.
// What: TestUpgradeBetweenReleases, TestUpgradeRefusals and
// TestUpgradeSelfContained, plus mk toolchain list, use and remove.
// How: serves two signed releases from httptest, signed with the test key in
// internal/release/testdata, and tampers with them.

package driver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"mk/internal/release"
)

// mk upgrade between two releases served by httptest, with the refusals (a
// wrong signature, a tampered SHA256SUMS, a hash mismatch, a truncated
// signature) and an interrupted unpack each leaving current where it was,
// and mk toolchain list, use and remove. Every root is under the test's
// temporary directory; no test reads MK_HOME, LOCALAPPDATA or PATH.

func upgradeTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "release", "testdata", "test.key"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := release.ParsePrivate(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func upgradeHost() string { return release.HostOf(runtime.GOOS, runtime.GOARCH) }

func mkExeName() string {
	if runtime.GOOS == "windows" {
		return "mk.exe"
	}
	return "mk"
}

// releaseServer serves releases in GitHub's layout; files can be replaced
// per test.
type releaseServer struct {
	mu     sync.Mutex
	files  map[string][]byte // URL path -> body
	srv    *httptest.Server
	served []string
}

func (s *releaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b, ok := s.files[r.URL.Path]
	s.served = append(s.served, r.URL.Path)
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(b)
}

func (s *releaseServer) set(p string, b []byte) {
	s.mu.Lock()
	s.files[p] = b
	s.mu.Unlock()
}

func archiveBytes(t *testing.T, version, host string) []byte {
	t.Helper()
	var b bytes.Buffer
	err := release.WriteArchive(&b, release.TopDir(version, host), []release.Entry{
		{Name: "bin/" + mkExeName(), Data: []byte("mk " + version), Exec: true},
		{Name: "llvm/bin/clang", Data: []byte("clang " + version), Exec: true},
		{Name: "RELEASE.txt", Data: []byte("mk " + version + "\n")},
	}, host == release.HostWindows)
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// publish puts one release on the server: its archive, SHA256SUMS and
// signature; latest also serves it under latest/download.
func (s *releaseServer) publish(t *testing.T, priv ed25519.PrivateKey, version string, latest bool) {
	host := upgradeHost()
	name := release.ArchiveName(version, host)
	arc := archiveBytes(t, version, host)
	sums := release.Sums{name: sha256hex(arc)}.Format()
	sig := release.Sign(priv, sums)
	for _, dir := range []string{"/download/v" + version + "/", "/latest/download/"} {
		if dir == "/latest/download/" && !latest {
			continue
		}
		s.set(dir+name, arc)
		s.set(dir+release.SumsFile, sums)
		s.set(dir+release.SigFile, sig)
	}
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func newReleaseServer(t *testing.T) *releaseServer {
	s := &releaseServer{files: map[string][]byte{}}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

// upgradeCfg is a config for a script install under root, trusting the
// test key, reading no environment.
func upgradeCfg(s *releaseServer, root, version string, priv ed25519.PrivateKey) UpgradeConfig {
	return UpgradeConfig{
		Version: version, Base: s.srv.URL, Home: root,
		Exe:    filepath.Join(root, "elsewhere", "bin", mkExeName()),
		Keys:   []release.Key{release.PublicOf(priv)},
		Getenv: func(string) string { return "" },
		// Never the real Release, which changes with every release.
		Running: testRunning,
	}
}

// testRunning is the running mk's version the tests inject.
const testRunning = "0.1.0"

// snapshot is every path under root, for "left everything as it was".
func snapshot(t *testing.T, root string) string {
	var b strings.Builder
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		r, _ := filepath.Rel(root, p)
		b.WriteString(r + "\n")
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			tg, _ := os.Readlink(p)
			b.WriteString("  -> " + tg + "\n")
			return filepath.SkipDir
		}
		return nil
	})
	return b.String()
}

func TestUpgradeBetweenReleases(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.3.0", false)
	s.publish(t, priv, "0.4.0", true)
	root := t.TempDir()
	res, err := Upgrade(upgradeCfg(s, root, "0.3.0", priv))
	if err != nil {
		t.Fatal(err)
	}
	in := release.Install{Root: root}
	if cur, _ := in.Current(); cur != "0.3.0" || res.New != "0.3.0" || res.Old != testRunning || !res.Installed {
		t.Errorf("first upgrade: %+v, current %q", res, cur)
	}
	if b, err := os.ReadFile(filepath.Join(in.CurrentLink(), "bin", mkExeName())); err != nil || string(b) != "mk 0.3.0" {
		t.Errorf("through current: %q %v", b, err)
	}
	// The newest release, through latest/download for SHA256SUMS and the
	// version's own address for the archive.
	res, err = Upgrade(upgradeCfg(s, root, "", priv))
	if err != nil {
		t.Fatal(err)
	}
	if cur, _ := in.Current(); cur != "0.4.0" || res.Old != "0.3.0" || res.New != "0.4.0" {
		t.Errorf("newest: %+v, current %q", res, cur)
	}
	if !strings.Contains(strings.Join(s.served, " "), "/latest/download/"+release.SumsFile) {
		t.Errorf("newest did not read latest: %v", s.served)
	}
	// Back to an unpacked version: no download.
	n := len(s.served)
	if res, err = Upgrade(upgradeCfg(s, root, "0.3.0", priv)); err != nil || !res.Reused {
		t.Errorf("reuse: %+v %v", res, err)
	}
	for _, p := range s.served[n:] {
		if strings.HasSuffix(p, ".zip") || strings.HasSuffix(p, ".tar.gz") {
			t.Errorf("downloaded %s again", p)
		}
	}
	// The version the running mk lives in is never removed, nor current.
	list, err := ToolchainList(ToolchainConfig{Home: root, Exe: filepath.Join(in.Dir("0.4.0"), "bin", mkExeName())})
	if err != nil || len(list) != 2 || list[0] != (InstalledToolchain{"0.3.0", true}) || list[1] != (InstalledToolchain{"0.4.0", false}) {
		t.Errorf("list: %v %v", list, err)
	}
	running := ToolchainConfig{Home: root, Exe: filepath.Join(in.Dir("0.4.0"), "bin", mkExeName())}
	if err := ToolchainRemove(running, "0.4.0"); err == nil {
		t.Error("removed the running mk's version")
	}
	if err := ToolchainRemove(running, "0.3.0"); err == nil {
		t.Error("removed current")
	}
	if err := ToolchainUse(running, "0.4.0"); err != nil {
		t.Fatal(err)
	}
	other := ToolchainConfig{Home: root, Exe: filepath.Join(t.TempDir(), "bin", mkExeName())}
	if err := ToolchainRemove(other, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if list, _ := ToolchainList(other); len(list) != 1 || list[0] != (InstalledToolchain{"0.4.0", true}) {
		t.Errorf("after remove: %v", list)
	}
	if _, err := ToolchainList(ToolchainConfig{Exe: filepath.Join(t.TempDir(), "bin", mkExeName()), Getenv: func(string) string { return "" }}); err == nil {
		t.Error("toolchain list outside any install")
	}
}

// TestUpgradeRefusals: each refusal leaves the root as it was.
func TestUpgradeRefusals(t *testing.T) {
	if upgradeHost() == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	_, otherPub, _ := release.GenerateKey()
	host := upgradeHost()
	name := release.ArchiveName("0.4.0", host)
	base := "/download/v0.4.0/"
	cases := []struct {
		name   string
		mangle func(s *releaseServer)
		cfg    func(c *UpgradeConfig)
		want   string
	}{
		{"wrong key", nil, func(c *UpgradeConfig) { c.Keys = []release.Key{otherPub} }, "does not trust"},
		{"no keys", nil, func(c *UpgradeConfig) { c.Keys = []release.Key{} }, "trusts no release key"},
		{"tampered sums", func(s *releaseServer) {
			b := append([]byte(nil), s.files[base+release.SumsFile]...)
			b[0] ^= 1
			s.set(base+release.SumsFile, b)
		}, nil, "signature does not match"},
		{"truncated signature", func(s *releaseServer) {
			b := s.files[base+release.SigFile]
			s.set(base+release.SigFile, b[:len(b)/2])
		}, nil, "signature does not match"},
		{"hash mismatch", func(s *releaseServer) {
			b := append([]byte(nil), s.files[base+name]...)
			b[len(b)/2] ^= 0xff
			s.set(base+name, b)
		}, nil, "SHA-256"},
		{"missing archive", func(s *releaseServer) { delete(s.files, base+name) }, nil, "404"},
		{"no such version", nil, func(c *UpgradeConfig) { c.Version = "9.9.9" }, "404"},
		{"interrupted unpack", nil, func(c *UpgradeConfig) {
			n := 0
			upgradeOnFile = func(string) error {
				if n++; n == 2 {
					return errors.New("interrupted")
				}
				return nil
			}
		}, "interrupted"},
		{"plain http elsewhere", nil, func(c *UpgradeConfig) { c.Base = "http://example.invalid/releases" }, "not https"},
		{"no address", nil, func(c *UpgradeConfig) { c.Base = ""; c.Getenv = func(string) string { return "" } }, "MK_RELEASES"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() { upgradeOnFile = nil }()
			s := newReleaseServer(t)
			s.publish(t, priv, "0.3.0", false)
			s.publish(t, priv, "0.4.0", true)
			root := t.TempDir()
			if _, err := Upgrade(upgradeCfg(s, root, "0.3.0", priv)); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, root)
			if c.mangle != nil {
				c.mangle(s)
			}
			cfg := upgradeCfg(s, root, "0.4.0", priv)
			if c.cfg != nil {
				c.cfg(&cfg)
			}
			if cfg.Base == "" && c.name == "no address" && release.Base() != "" {
				t.Skip("SKIP: this binary has a compiled-in release address")
			}
			_, err := Upgrade(cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if after := snapshot(t, root); after != before {
				t.Errorf("the refusal changed the root:\nbefore\n%s\nafter\n%s", before, after)
			}
			if cur, _ := (release.Install{Root: root}).Current(); cur != "0.3.0" {
				t.Errorf("current moved to %q", cur)
			}
		})
	}
}

// TestUpgradeSelfContained: an mk from an unpacked archive, not a script
// install, gets the new version beside its own directory, and nothing
// else changes.
func TestUpgradeSelfContained(t *testing.T) {
	host := upgradeHost()
	if host == "" {
		t.Skipf("SKIP: no release host for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	priv := upgradeTestKey(t)
	s := newReleaseServer(t)
	s.publish(t, priv, "0.4.0", true)
	parent := t.TempDir()
	own := filepath.Join(parent, release.TopDir("0.2.0", host), "bin")
	os.MkdirAll(own, 0o755)
	exe := filepath.Join(own, mkExeName())
	os.WriteFile(exe, []byte("mk 0.2.0"), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(own), "RELEASE.txt"), []byte("mk 0.2.0\n"), 0o644)
	res, err := Upgrade(UpgradeConfig{Base: s.srv.URL, Exe: exe, Keys: []release.Key{release.PublicOf(priv)}, Getenv: func(string) string { return "" }, Running: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, release.TopDir("0.4.0", host))
	if res.Installed || res.Old != "0.2.0" || res.New != "0.4.0" || !samePathFold(res.Dir, want) {
		t.Errorf("self-contained: %+v, want dir %s", res, want)
	}
	if b, err := os.ReadFile(filepath.Join(want, "bin", mkExeName())); err != nil || string(b) != "mk 0.4.0" {
		t.Errorf("unpacked: %q %v", b, err)
	}
	des, _ := os.ReadDir(parent)
	if len(des) != 2 {
		t.Errorf("parent holds %d entries, want the two versions", len(des))
	}
	if b, _ := os.ReadFile(exe); string(b) != "mk 0.2.0" {
		t.Error("the running mk changed")
	}
}

func samePathFold(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
