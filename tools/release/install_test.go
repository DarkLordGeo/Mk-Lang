// Why: the install scripts run on users' machines with network input, so they
// must install and refuse bad releases without touching more than their
// root.
// What: tests of tools/install/install.ps1 (Windows) and install.sh (Linux):
// install, newest version, tampering, hardening and PATH handling.
// How: each script runs against a local httptest release server into the
// test's temporary directory, PATH changes off and every home redirected;
// install.ps1's registry write is never run (Join-MkPath is tested instead).

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/release"
)

type scriptServer struct {
	mu        sync.Mutex
	files     map[string][]byte
	redirects map[string]string // URL path -> Location
	requests  []string
}

func (s *scriptServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.URL.Path)
	b, ok := s.files[r.URL.Path]
	loc, redir := s.redirects[r.URL.Path]
	s.mu.Unlock()
	if redir {
		http.Redirect(w, r, loc, http.StatusFound)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(b)
}

func (s *scriptServer) redirect(p, loc string) {
	s.mu.Lock()
	if s.redirects == nil {
		s.redirects = map[string]string{}
	}
	s.redirects[p] = loc
	s.mu.Unlock()
}

func (s *scriptServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *scriptServer) set(p string, b []byte) {
	s.mu.Lock()
	s.files[p] = b
	s.mu.Unlock()
}

// publish serves one release of host's archive under download/v<version>
// and, when latest, under latest/download.
func (s *scriptServer) publish(t *testing.T, version, host string, latest bool) []byte {
	t.Helper()
	exe := "mk"
	if host == release.HostWindows {
		exe = "mk.exe"
	}
	var b bytes.Buffer
	if err := release.WriteArchive(&b, release.TopDir(version, host), []release.Entry{
		{Name: "bin/" + exe, Data: []byte("mk " + version), Exec: true},
		{Name: "llvm/bin/clang", Data: []byte("clang"), Exec: true},
	}, host == release.HostWindows); err != nil {
		t.Fatal(err)
	}
	name := release.ArchiveName(version, host)
	h := sha256.Sum256(b.Bytes())
	// Another host's line and a pre-release line, which the scripts skip
	// or rank below the release.
	other := release.HostLinux
	if host == release.HostLinux {
		other = release.HostWindows
	}
	sums := release.Sums{name: hex.EncodeToString(h[:]), release.ArchiveName("9.9.9", other): strings.Repeat("0", 64),
		release.ArchiveName(version+"-rc1", host): strings.Repeat("1", 64)}.Format()
	for _, d := range []string{"/download/v" + version + "/", "/latest/download/"} {
		if d == "/latest/download/" && !latest {
			continue
		}
		s.set(d+name, b.Bytes())
		s.set(d+release.SumsFile, sums)
	}
	return b.Bytes()
}

// scratchEnv is the child's environment: the host's, without MK_* and
// with every per-user location redirected into dir.
func scratchEnv(dir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "LOCALAPPDATA", "USERPROFILE", "APPDATA", "HOME", "XDG_DATA_HOME":
			continue
		}
		if strings.HasPrefix(strings.ToUpper(k), "MK_") {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range []string{"LOCALAPPDATA", "USERPROFILE", "APPDATA", "HOME"} {
		p := filepath.Join(dir, "profile", strings.ToLower(k))
		os.MkdirAll(p, 0o755)
		env = append(env, k+"="+p)
	}
	// A script that ignored -NoModifyPath / --no-modify-path still leaves
	// the registry and ~/.profile alone.
	return append(env, "MK_NO_MODIFY_PATH=1")
}

func powershell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: install.ps1 runs on Windows")
	}
	p, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skipf("SKIP: no powershell.exe: %v", err)
	}
	return p
}

func scriptPath(t *testing.T, name string) string {
	p, err := filepath.Abs(filepath.Join("..", "install", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallPs1(t *testing.T) {
	ps := powershell(t)
	s := &scriptServer{files: map[string][]byte{}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.publish(t, "0.3.0", release.HostWindows, false)
	s.publish(t, "0.4.0", release.HostWindows, true)
	dir := t.TempDir()
	root := filepath.Join(dir, "mkroot")
	run := func(extra ...string) (string, error) {
		args := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath(t, "install.ps1"),
			"-Root", root, "-Releases", srv.URL, "-NoModifyPath"}, extra...)
		cmd := exec.Command(ps, args...)
		cmd.Env = scratchEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	in := release.Install{Root: root}
	out, err := run("-Version", "0.3.0")
	if err != nil {
		t.Fatalf("install 0.3.0: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Fatalf("current %q after installing 0.3.0:\n%s", cur, out)
	}
	if b, err := os.ReadFile(filepath.Join(root, "current", "bin", "mk.exe")); err != nil || string(b) != "mk 0.3.0" {
		t.Errorf("through current: %q %v", b, err)
	}
	if !strings.Contains(out, "PATH was not changed") || !strings.Contains(out, filepath.Join(root, `current\bin`)) {
		t.Errorf("no PATH line:\n%s", out)
	}
	// The newest (0.4.0, not its rc1, not another host's 9.9.9).
	if out, err = run(); err != nil {
		t.Fatalf("install newest: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.4.0" {
		t.Errorf("current %q after the newest:\n%s", cur, out)
	}
	// Back to 0.3.0: already unpacked, current moves.
	if out, err = run("-Version", "0.3.0"); err != nil || !strings.Contains(out, "already") {
		t.Errorf("reinstall: %v\n%s", err, out)
	}
	if vs, _ := in.Versions(); len(vs) != 2 {
		t.Errorf("versions %v", vs)
	}
	// A tampered archive is refused and nothing changes.
	name := release.ArchiveName("0.5.0", release.HostWindows)
	good := s.publish(t, "0.5.0", release.HostWindows, true)
	bad := append([]byte(nil), good...)
	bad[len(bad)/2] ^= 0xff
	s.set("/download/v0.5.0/"+name, bad)
	if out, err = run(); err == nil || !strings.Contains(out, "SHA-256") {
		t.Errorf("tampered archive: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Errorf("a refused install moved current to %q", cur)
	}
	if vs, _ := in.Versions(); len(vs) != 2 {
		t.Errorf("a refused install left %v", vs)
	}
	des, _ := os.ReadDir(in.Toolchains())
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			t.Errorf("left %s", de.Name())
		}
	}
	// Plain http to a host that is not loopback is refused.
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath(t, "install.ps1"),
		"-Root", root, "-Releases", "http://example.invalid/releases", "-NoModifyPath")
	cmd.Env = scratchEnv(dir)
	if b, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(b), "not https") {
		t.Errorf("http elsewhere: %v\n%s", err, b)
	}
	// Nothing reached the redirected profile directories.
	for _, k := range []string{"localappdata", "userprofile", "appdata", "home"} {
		if des, _ := os.ReadDir(filepath.Join(dir, "profile", k)); len(des) != 0 {
			t.Errorf("the script wrote into %s: %v", k, des)
		}
	}
}

// TestInstallPs1PathString: the user PATH value install.ps1 would write,
// from its Join-MkPath through the hidden -JoinPathOnly switch (no
// install, no registry).
func TestInstallPs1PathString(t *testing.T) {
	ps := powershell(t)
	sp := scriptPath(t, "install.ps1")
	script := `& '` + sp + `' -JoinPathOnly 'C:\a;C:\b','C:\m\current\bin'
& '` + sp + `' -JoinPathOnly 'C:\a;C:\M\Current\Bin\;C:\b','C:\m\current\bin'
& '` + sp + `' -JoinPathOnly '','C:\m\current\bin'
`
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = scratchEnv(t.TempDir())
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	want := "C:\\m\\current\\bin;C:\\a;C:\\b\r\nC:\\a;C:\\M\\Current\\Bin\\;C:\\b\r\nC:\\m\\current\\bin\r\n"
	if string(b) != want {
		t.Errorf("Join-MkPath:\n%q\nwant\n%q", b, want)
	}
}

// TestInstallPs1Hardening: a version that
// is not a version token is refused before any request; a redirect to
// plain http elsewhere is refused; a redirect on the loopback server is
// followed; a relative -Root is made absolute; and `irm | iex` (the script
// text run in the caller's session) leaves no preference, strict mode,
// function or variable behind.
func TestInstallPs1Hardening(t *testing.T) {
	ps := powershell(t)
	s := &scriptServer{files: map[string][]byte{}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.publish(t, "0.3.0", release.HostWindows, false)
	s.publish(t, "0.4.0", release.HostWindows, false)
	dir := t.TempDir()
	root := filepath.Join(dir, "mkroot")
	run := func(cwd string, extra ...string) (string, error) {
		args := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath(t, "install.ps1"),
			"-Releases", srv.URL, "-NoModifyPath"}, extra...)
		cmd := exec.Command(ps, args...)
		cmd.Env = scratchEnv(dir)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, v := range []string{"../evil", "0.3.0/../../x", "0.3", "0.3.0-rc1/x"} {
		n := len(s.requested())
		out, err := run(dir, "-Root", root, "-Version", v)
		if err == nil || !strings.Contains(out, "not a version") {
			t.Errorf("-Version %q: %v\n%s", v, err, out)
		}
		if got := s.requested()[n:]; len(got) != 0 {
			t.Errorf("-Version %q requested %v", v, got)
		}
	}
	// GitHub's newest address redirects; the script follows on loopback.
	s.redirect("/latest/download/"+release.SumsFile, "/download/v0.4.0/"+release.SumsFile)
	if out, err := run(dir, "-Root", root); err != nil || !strings.Contains(out, "0.4.0") {
		t.Fatalf("a loopback redirect: %v\n%s", err, out)
	}
	// A redirect to plain http elsewhere is refused, and nothing changes.
	s.redirect("/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostWindows), "http://example.invalid/x.zip")
	out, err := run(dir, "-Root", root, "-Version", "0.3.0")
	if err == nil || !strings.Contains(out, "not https") {
		t.Errorf("a redirect to http: %v\n%s", err, out)
	}
	in := release.Install{Root: root}
	if cur, _ := in.Current(); cur != "0.4.0" {
		t.Errorf("current %q after a refused redirect", cur)
	}
	// A relative -Root, from another working directory.
	cwd := filepath.Join(dir, "cwd")
	os.MkdirAll(cwd, 0o755)
	if out, err := run(cwd, "-Root", "rel", "-Version", "0.4.0"); err != nil {
		t.Fatalf("relative root: %v\n%s", err, out)
	}
	rel := release.Install{Root: filepath.Join(cwd, "rel")}
	tgt, err := os.Readlink(rel.CurrentLink())
	if err != nil || !filepath.IsAbs(tgt) {
		t.Errorf("relative root's current: %q %v", tgt, err)
	}
	if b, err := os.ReadFile(filepath.Join(rel.CurrentLink(), "bin", "mk.exe")); err != nil || string(b) != "mk 0.4.0" {
		t.Errorf("through the relative root's current: %q %v", b, err)
	}
	// irm | iex: the text run in the caller's session.
	script := `$env:MK_HOME = '` + filepath.Join(dir, "iexroot") + `'
$env:MK_RELEASES = '` + srv.URL + `'
$env:MK_VERSION = '0.3.0'
Get-Content -Raw -LiteralPath '` + scriptPath(t, "install.ps1") + `' | Invoke-Expression
"EAP=$ErrorActionPreference"
"FN=$([bool](Get-Command Join-MkPath -ErrorAction SilentlyContinue))"
"VAR=$(Test-Path variable:MkHost)"
"PARAM=$(Test-Path variable:Releases)"
try { $undefinedVariable | Out-Null; "STRICT=off" } catch { "STRICT=on" }
`
	s.redirect("/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostWindows), "/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostWindows)+"x")
	s.set("/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostWindows)+"x", s.files["/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostWindows)])
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = scratchEnv(dir)
	b, err := cmd.CombinedOutput()
	o := string(b)
	if err != nil || !strings.Contains(o, "current is mk 0.3.0") {
		t.Fatalf("irm | iex: %v\n%s", err, o)
	}
	for _, want := range []string{"EAP=Continue", "FN=False", "VAR=False", "PARAM=False", "STRICT=off"} {
		if !strings.Contains(o, want) {
			t.Errorf("irm | iex leaked: want %s in\n%s", want, o)
		}
	}
}

// TestInstallSh runs install.sh on Linux: the install, the newest, a tampered
// archive, and --no-modify-path against ~/.profile (HOME is redirected).
func TestInstallSh(t *testing.T) {
	// MK_INSTALL_SH_ANYWHERE=1 runs it elsewhere too (a developer's check
	// under Git's sh on Windows with native symbolic links), never in place
	// of the Linux run.
	if runtime.GOOS != "linux" && os.Getenv("MK_INSTALL_SH_ANYWHERE") != "1" {
		t.Skip("SKIP: install.sh runs under the Linux leaves (MK_LINUX=1)")
	}
	s := &scriptServer{files: map[string][]byte{}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.publish(t, "0.3.0", release.HostLinux, false)
	s.publish(t, "0.4.0", release.HostLinux, true)
	dir := t.TempDir()
	// A root with a space.
	root := filepath.Join(dir, "mk root")
	// The ~/.profile leg needs PATH modification on: HOME is the redirected
	// one in dir, so only the test's own .profile is written.
	env := slices.DeleteFunc(scratchEnv(dir), func(kv string) bool { return kv == "MK_NO_MODIFY_PATH=1" })
	home := filepath.Join(dir, "profile", "home")
	run := func(extra ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{scriptPath(t, "install.sh"), "--root", root, "--releases", srv.URL}, extra...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	in := release.Install{Root: root}
	out, err := run("--version", "0.3.0", "--no-modify-path")
	if err != nil {
		t.Fatalf("install 0.3.0: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Fatalf("current %q:\n%s", cur, out)
	}
	fi, err := os.Stat(filepath.Join(root, "current", "bin", "mk"))
	if err != nil || runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("bin/mk: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".profile")); err == nil {
		t.Error("--no-modify-path wrote ~/.profile")
	}
	// A backslash in root (a Windows path under Git's sh) is doubled.
	if !strings.Contains(out, `export PATH="`+strings.ReplaceAll(root, `\`, `\\`)+`/current/bin:$PATH"`) {
		t.Errorf("no PATH line:\n%s", out)
	}
	if out, err = run(); err != nil {
		t.Fatalf("newest: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.4.0" {
		t.Errorf("current %q after the newest:\n%s", cur, out)
	}
	prof, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil || strings.Count(string(prof), "export PATH=") != 1 {
		t.Errorf("~/.profile: %q %v", prof, err)
	}
	if out, err = run("--version", "0.3.0"); err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out)
	}
	if prof2, _ := os.ReadFile(filepath.Join(home, ".profile")); !bytes.Equal(prof, prof2) {
		t.Error("~/.profile got the line twice")
	}
	name := release.ArchiveName("0.5.0", release.HostLinux)
	good := s.publish(t, "0.5.0", release.HostLinux, true)
	bad := append([]byte(nil), good...)
	bad[len(bad)/2] ^= 0xff
	s.set("/download/v0.5.0/"+name, bad)
	if out, err = run("--no-modify-path"); err == nil || !strings.Contains(out, "SHA-256") {
		t.Errorf("tampered archive: %v\n%s", err, out)
	}
	if cur, _ := in.Current(); cur != "0.3.0" {
		t.Errorf("a refused install moved current to %q", cur)
	}
	if vs, _ := in.Versions(); len(vs) != 2 {
		t.Errorf("a refused install left %v", vs)
	}
}

// TestInstallShHardening runs install.sh like
// TestInstallSh (Linux; MK_INSTALL_SH_ANYWHERE=1 elsewhere as a developer
// check): a version that is not a version token is refused before any
// request; a redirect to plain http elsewhere is refused (curl, and wget
// when curl is hidden); a loopback redirect is followed; a relative --root
// is made absolute; a root that a profile line cannot hold is refused
// before ~/.profile is touched.
func TestInstallShHardening(t *testing.T) {
	if runtime.GOOS != "linux" && os.Getenv("MK_INSTALL_SH_ANYWHERE") != "1" {
		t.Skip("SKIP: install.sh runs under the Linux leaves (MK_LINUX=1)")
	}
	s := &scriptServer{files: map[string][]byte{}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	s.publish(t, "0.3.0", release.HostLinux, false)
	s.publish(t, "0.4.0", release.HostLinux, false)
	dir := t.TempDir()
	root := filepath.Join(dir, "mkroot")
	run := func(cwd string, env []string, extra ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{scriptPath(t, "install.sh"), "--releases", srv.URL}, extra...)...)
		cmd.Env = env
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	env := scratchEnv(dir)
	for _, v := range []string{"../evil", "0.3.0/../../x", "0.3", "0.3.0-rc1/x"} {
		n := len(s.requested())
		out, err := run(dir, env, "--root", root, "--version", v)
		if err == nil || !strings.Contains(out, "not a version") {
			t.Errorf("--version %q: %v\n%s", v, err, out)
		}
		if got := s.requested()[n:]; len(got) != 0 {
			t.Errorf("--version %q requested %v", v, got)
		}
	}
	s.redirect("/latest/download/"+release.SumsFile, "/download/v0.4.0/"+release.SumsFile)
	if out, err := run(dir, env, "--root", root); err != nil || !strings.Contains(out, "current is mk 0.4.0") {
		t.Fatalf("a loopback redirect: %v\n%s", err, out)
	}
	s.redirect("/download/v0.3.0/"+release.ArchiveName("0.3.0", release.HostLinux), "http://example.invalid/x.tar.gz")
	if out, err := run(dir, env, "--root", root, "--version", "0.3.0"); err == nil || !strings.Contains(out, "not https") {
		t.Errorf("a redirect to http (curl): %v\n%s", err, out)
	}
	// wget alone: a PATH directory with wget but no curl.
	if wget, err := exec.LookPath("wget"); err == nil {
		bin := filepath.Join(dir, "wgetonly")
		os.MkdirAll(bin, 0o755)
		for _, tool := range []string{"sh", "grep", "sed", "awk", "sort", "tail", "cut", "mktemp", "rm", "mkdir", "mv", "ln", "gzip", "tar", "sha256sum", "cat", "dirname", "pwd", "tr", "head"} {
			if p, err := exec.LookPath(tool); err == nil {
				os.Symlink(p, filepath.Join(bin, filepath.Base(p)))
			}
		}
		os.Symlink(wget, filepath.Join(bin, "wget"))
		wenv := append(slices.DeleteFunc(append([]string(nil), env...), func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }), "PATH="+bin)
		if out, err := run(dir, wenv, "--root", root, "--version", "0.3.0"); err == nil || !strings.Contains(out, "not https") {
			t.Errorf("a redirect to http (wget): %v\n%s", err, out)
		}
	} else {
		t.Log("NOT RUN: the wget leg (no wget on PATH)")
	}
	if cur, _ := (release.Install{Root: root}).Current(); cur != "0.4.0" {
		t.Errorf("current %q after refused installs", cur)
	}
	// A relative --root, from another working directory.
	cwd := filepath.Join(dir, "cwd")
	os.MkdirAll(cwd, 0o755)
	if out, err := run(cwd, env, "--root", "rel", "--version", "0.4.0"); err != nil {
		t.Fatalf("relative root: %v\n%s", err, out)
	}
	tgt, err := os.Readlink(filepath.Join(cwd, "rel", "current"))
	if err != nil || !(filepath.IsAbs(tgt) || strings.HasPrefix(tgt, "/")) {
		t.Errorf("relative root's current: %q %v", tgt, err)
	}
	// A root a profile line cannot hold, with PATH modification on.
	home := filepath.Join(dir, "profile", "home")
	penv := slices.DeleteFunc(append([]string(nil), env...), func(kv string) bool { return kv == "MK_NO_MODIFY_PATH=1" })
	if runtime.GOOS == "windows" {
		// Windows' command line cannot carry a quote to Git's sh intact.
		t.Log("NOT RUN: the profile-line roots (Linux only)")
		return
	}
	for _, bad := range []string{`a"b`, "a$b", "a`b"} {
		out, err := run(dir, penv, "--root", filepath.Join(dir, bad), "--version", "0.4.0")
		if err == nil || !strings.Contains(out, "profile line") {
			t.Errorf("root %q: %v\n%s", bad, err, out)
		}
		if _, err := os.Stat(filepath.Join(home, ".profile")); err == nil {
			t.Errorf("root %q: ~/.profile written", bad)
		}
	}
}
