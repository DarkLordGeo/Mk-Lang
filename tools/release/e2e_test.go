// Why: signing, installing and upgrading only protect users if they work
// together: a release mk must refuse every bad release it is offered.
// What: TestReleaseEndToEnd: keygen, sums and sign, an install script, and mk
// upgrade against good, tampered and wrongly signed releases.
// How: mk is built with -overlay to trust a synthetic key and served from a
// local fake release server; nothing touches the network or the real key.

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/release"
)

// TestReleaseEndToEnd strings release, install and upgrade together
// against synthetic material only: a key pair `keygen`
// writes into the test's temporary directory; two synthetic releases
// (7.1.0 and 7.2.0) whose archives carry a real mk, summed and signed by
// `sums` and `sign`; a local fake release server (httptest on loopback);
// install.ps1 (Windows) or install.sh (Linux) installing 7.1.0 into a
// temporary root; and the installed mk's own `mk upgrade` moving to 7.2.0.
// Then `mk upgrade` refuses, each time leaving current and the toolchains
// as they were: a tampered archive, a release signed by a key it does not
// trust, one signed by the repository's test key, a truncated signature,
// and an unpack that fails part way (an archive whose last file is
// corrupt, summed and signed as served).
//
// The release key is compiled into mk by a test build: `go build -overlay`
// replaces internal/release/keys.go's trusted list with the synthetic
// public key (the repository's file is not touched), and -ldflags sets a
// synthetic release address, so the mk is a release build, which refuses
// the test key; MK_RELEASES points it at the fake server. The versions are
// synthetic and unrelated to the running one. An mk built
// without the overlay trusts only the release keys and refuses a release
// the test key signed (fails closed).
func TestReleaseEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: -short (builds mk twice)")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	host := release.HostOf(runtime.GOOS, runtime.GOARCH)
	exe := ""
	if host == release.HostWindows {
		exe = ".exe"
	}

	// The synthetic key, never outside the temporary directory.
	keyFile := filepath.Join(root, "keys", "synthetic.key")
	os.MkdirAll(filepath.Dir(keyFile), 0o700)
	var kout, kerr bytes.Buffer
	if st := run([]string{"keygen", "-out", keyFile}, &kout, &kerr); st != 0 {
		t.Fatalf("keygen: %s", kerr.String())
	}
	m := regexp.MustCompile(`\{ID: "[0-9a-f]+", Public: "[^"]+"\},`).FindString(kout.String())
	if m == "" {
		t.Fatalf("keygen printed no key line:\n%s", kout.String())
	}
	privData, _ := os.ReadFile(keyFile)
	priv, err := release.ParsePrivate(privData)
	if err != nil {
		t.Fatal(err)
	}

	// mk with the synthetic key (overlay) and a synthetic release address.
	keysGo := filepath.Join(repo, "internal", "release", "keys.go")
	src, err := os.ReadFile(keysGo)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)var trusted = \[\]Key\{(\}|\n.*?\n\})`)
	if !re.Match(src) {
		t.Fatal("internal/release/keys.go has no `var trusted = []Key{...}` to overlay")
	}
	ovKeys := filepath.Join(root, "overlay", "keys.go")
	os.MkdirAll(filepath.Dir(ovKeys), 0o755)
	os.WriteFile(ovKeys, re.ReplaceAll(src, []byte("var trusted = []Key{\n\t"+m+"\n}")), 0o644)
	ov, _ := json.Marshal(map[string]map[string]string{"Replace": {keysGo: ovKeys}})
	ovJSON := filepath.Join(root, "overlay", "overlay.json")
	os.WriteFile(ovJSON, ov, 0o644)
	const syntheticBase = "https://releases.mk-test.invalid/mk"
	goBuildMkAt := func(out string, extra ...string) {
		t.Helper()
		args := append([]string{"build", "-trimpath", "-o", out}, extra...)
		cmd := exec.Command(goTool(t), append(args, "./cmd/mk")...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", extra, err, b)
		}
	}
	signedMk := filepath.Join(root, "build", "signed", "mk"+exe)
	goBuildMkAt(signedMk, "-overlay", ovJSON, "-ldflags", "-X mk/internal/release.base="+syntheticBase)
	plainMk := filepath.Join(root, "build", "plain", "mk"+exe)
	goBuildMkAt(plainMk, "-ldflags", "-X mk/internal/release.base="+syntheticBase)

	// A release directory: the host's archive (the signed mk, a payload of
	// random bytes as its last file), SHA256SUMS and the signature.
	nRel := 0
	payload := make([]byte, 64<<10)
	rand.Read(payload)
	makeRelease := func(version string, edit func(archive []byte) []byte, sign func(dir string)) string {
		t.Helper()
		nRel++
		dir := filepath.Join(root, "releases", fmt.Sprintf("%s-%d", version, nRel))
		os.MkdirAll(dir, 0o755)
		var b bytes.Buffer
		if err := release.WriteArchive(&b, release.TopDir(version, host), []release.Entry{
			{Name: "bin/mk" + exe, Path: signedMk, Exec: true},
			{Name: "RELEASE.txt", Data: []byte("mk " + version + " (synthetic)\n")},
			{Name: "zz/payload.bin", Data: payload},
		}, host == release.HostWindows); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		if edit != nil {
			data = edit(data)
		}
		os.WriteFile(filepath.Join(dir, release.ArchiveName(version, host)), data, 0o644)
		if n, err := writeSums(dir); err != nil || n != 1 {
			t.Fatalf("sums: %d %v", n, err)
		}
		if sign != nil {
			sign(dir)
		} else if _, err := signDir(priv, dir, false); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	s := &scriptServer{files: map[string][]byte{}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	serve := func(version, dir string, latest bool) {
		t.Helper()
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			b, _ := os.ReadFile(filepath.Join(dir, de.Name()))
			s.set("/download/v"+version+"/"+de.Name(), b)
			if latest {
				s.set("/latest/download/"+de.Name(), b)
			}
		}
	}
	r71 := makeRelease("7.1.0", nil, nil)
	r72 := makeRelease("7.2.0", nil, nil)
	if k, n, err := verifyDir(r72, []release.Key{release.PublicOf(priv)}); err != nil || n != 1 || k.ID != release.PublicOf(priv).ID {
		t.Fatalf("verify the synthetic release: %v %d %v", k, n, err)
	}
	serve("7.1.0", r71, false)
	serve("7.2.0", r72, true)

	// Install 7.1.0 with the host's script.
	home := filepath.Join(root, "mkhome")
	// The script trusts the synthetic key (the last MK_INSTALL_TEST_KEY
	// wins over scratchEnv's test key).
	pub := release.PublicOf(priv)
	env := append(scratchEnv(filepath.Join(root, "user")), "MK_INSTALL_TEST_KEY="+pub.ID+" "+pub.Public)
	switch host {
	case release.HostWindows:
		ps := powershell(t)
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath(t, "install.ps1"),
			"-Root", home, "-Releases", srv.URL, "-Version", "7.1.0", "-NoModifyPath")
		cmd.Env = env
		if b, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(b), "current is mk 7.1.0") {
			t.Fatalf("install.ps1: %v\n%s", err, b)
		}
	default:
		cmd := exec.Command("sh", scriptPath(t, "install.sh"), "--root", home, "--releases", srv.URL, "--version", "7.1.0", "--no-modify-path")
		cmd.Env = env
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install.sh: %v\n%s", err, b)
		}
	}
	in := release.Install{Root: home}
	state := func() string {
		cur, _ := in.Current()
		vs, _ := in.Versions()
		var stray []string
		des, _ := os.ReadDir(in.Toolchains())
		for _, de := range des {
			if strings.HasPrefix(de.Name(), ".") {
				stray = append(stray, de.Name())
			}
		}
		des, _ = os.ReadDir(home)
		for _, de := range des {
			if strings.HasPrefix(de.Name(), ".") {
				stray = append(stray, de.Name())
			}
		}
		return fmt.Sprintf("current %s, versions %v, strays %v", cur, vs, stray)
	}
	if got := state(); got != "current 7.1.0, versions [7.1.0], strays []" {
		t.Fatalf("after the install: %s", got)
	}

	// The installed mk upgrades itself, through current.
	installed := filepath.Join(in.CurrentLink(), "bin", "mk"+exe)
	mkEnv := append(throwawayEnv(filepath.Join(root, "mkenv")), "MK_RELEASES="+srv.URL)
	mk := func(bin string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = mkEnv
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out, err := mk(installed, "upgrade")
	if err != nil || !strings.Contains(out, "mk 7.1.0 -> 7.2.0") {
		t.Fatalf("mk upgrade: %v\n%s", err, out)
	}
	if got := state(); got != "current 7.2.0, versions [7.1.0 7.2.0], strays []" {
		t.Fatalf("after mk upgrade: %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(in.CurrentLink(), "RELEASE.txt")); err != nil || !strings.HasPrefix(string(b), "mk 7.2.0") {
		t.Errorf("through current: %q %v", b, err)
	}
	out, err = mk(installed, "toolchain", "list")
	if err != nil || !strings.Contains(out, "7.1.0") || !strings.Contains(out, "7.2.0") {
		t.Errorf("mk toolchain list: %v\n%s", err, out)
	}
	if out, err = mk(installed, "toolchain", "use", "7.1.0"); err != nil {
		t.Fatalf("mk toolchain use 7.1.0: %v\n%s", err, out)
	}
	if out, err = mk(installed, "upgrade", "7.2.0"); err != nil || !strings.Contains(out, "7.1.0 -> 7.2.0") {
		t.Fatalf("mk upgrade 7.2.0 (already unpacked): %v\n%s", err, out)
	}
	want := state()

	// The refusals, each served as the newest release 7.3.0.
	flip := func(at func(b []byte) int) func([]byte) []byte {
		return func(b []byte) []byte {
			b = append([]byte(nil), b...)
			b[at(b)] ^= 0xff
			return b
		}
	}
	wrongPriv, _, _ := release.GenerateKey()
	wrong, _ := release.ParsePrivate(wrongPriv)
	testPriv, err := release.ParsePrivate(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	// breakLastFile damages the payload, the last file: the files before
	// it are unpacked when the unpack fails. A zip stores it, so a flipped
	// byte fails its CRC; a tar.gz is cut short inside it (tar has no
	// checksum of a file's data). The archive is checked to fail
	// release.Extract here, so the case cannot pass vacuously.
	breakLastFile := func(b []byte) []byte { return breakLastFile(t, b, payload, "7.3.0", host) }
	cases := []struct {
		name  string
		edit  func([]byte) []byte
		sign  func(dir string)
		after func(dir string) // edits the release after signing
		want  string
	}{
		{name: "tampered archive", after: func(dir string) {
			p := filepath.Join(dir, release.ArchiveName("7.3.0", host))
			b, _ := os.ReadFile(p)
			os.WriteFile(p, flip(func(b []byte) int { return len(b) / 2 })(b), 0o644)
		}, want: "SHA-256"},
		{name: "wrong key", sign: func(dir string) { signDir(wrong, dir, false) }, want: "does not trust"},
		{name: "test key", sign: func(dir string) { signDir(testPriv, dir, true) }, want: "a release build refuses"},
		{name: "truncated signature", after: func(dir string) {
			p := filepath.Join(dir, release.SigFile)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, b[:len(b)/2], 0o644)
		}, want: "signature"},
		{name: "interrupted unpack", edit: breakLastFile, want: "nothing was changed"},
	}
	for i, c := range cases {
		dir := makeRelease("7.3.0", c.edit, c.sign)
		if c.after != nil {
			c.after(dir)
		}
		for k := range s.files {
			if strings.Contains(k, "7.3.0") || strings.HasPrefix(k, "/latest/") {
				delete(s.files, k)
			}
		}
		serve("7.3.0", dir, true)
		out, err := mk(installed, "upgrade")
		t.Logf("%s: %s", c.name, strings.TrimSpace(out))
		if err == nil || !strings.Contains(out, c.want) || !strings.Contains(out, "nothing was changed") {
			t.Errorf("%d %s: %v\n%s", i, c.name, err, out)
		}
		if got := state(); got != want {
			t.Errorf("%s changed the install: %s, want %s", c.name, got, want)
		}
	}

	// An mk that lists only the release keys fails closed on a release
	// signed by a key it does not list (the synthetic key).
	serve("7.2.0", r72, true)
	out, err = mk(plainMk, "upgrade")
	if err == nil || !strings.Contains(out, withoutTestKeyRefusal()) {
		t.Errorf("an mk without the synthetic key: %v\n%s", err, out)
	}
	if !slices.Contains(s.requested(), "/download/v7.1.0/"+release.ArchiveName("7.1.0", host)) {
		t.Errorf("the script did not download from the fake server: %v", s.requested())
	}
}

// breakLastFile damages payload, the last file of an archive of version
// for host: the files before it are unpacked when the unpack fails. A zip
// stores the random payload, so a flipped byte fails its CRC; a tar.gz is
// cut short inside it (tar has no checksum of a file's data). It checks
// that release.Extract fails on the result, so a case built on it cannot
// pass vacuously.
func breakLastFile(t *testing.T, b, payload []byte, version, host string) []byte {
	t.Helper()
	var out []byte
	if host == release.HostWindows {
		i := bytes.Index(b, payload[1000:1032])
		if i < 0 {
			t.Fatal("the payload is not stored verbatim in the zip")
		}
		out = append([]byte(nil), b...)
		out[i+16] ^= 0xff
	} else {
		out = append([]byte(nil), b[:len(b)-16<<10]...)
	}
	p := filepath.Join(t.TempDir(), release.ArchiveName(version, host))
	os.WriteFile(p, out, 0o644)
	var files []string
	err := release.Extract(p, filepath.Join(t.TempDir(), "x"), release.TopDir(version, host), release.Limits{}, func(n string) error {
		files = append(files, n)
		return nil
	})
	if err == nil {
		t.Fatalf("the broken %s archive unpacks", host)
	}
	t.Logf("the broken %s archive fails after %v: %v", host, files, err)
	return out
}

// TestBreakLastFile: the damage TestReleaseEndToEnd serves as an
// interrupted unpack fails part way in both archive formats, after the
// files before the payload.
func TestBreakLastFile(t *testing.T) {
	payload := make([]byte, 64<<10)
	rand.Read(payload)
	for _, host := range []string{release.HostWindows, release.HostLinux} {
		var b bytes.Buffer
		if err := release.WriteArchive(&b, release.TopDir("7.3.0", host), []release.Entry{
			{Name: "bin/mk", Data: []byte("mk"), Exec: true},
			{Name: "zz/payload.bin", Data: payload},
		}, host == release.HostWindows); err != nil {
			t.Fatal(err)
		}
		breakLastFile(t, b.Bytes(), payload, "7.3.0", host)
	}
}
