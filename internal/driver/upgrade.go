// Why: an installed mk must replace itself with a verified newer release,
// and keep several toolchain versions side by side, without ever moving an
// install back by accident.
//
// What: UpgradeConfig, UpgradeResult and Upgrade (mk upgrade), and
// ToolchainConfig with ToolchainList, ToolchainUse and ToolchainRemove (mk
// toolchain). cmd/mk reaches internal/release only through these functions.
//
// How: SHA256SUMS and its signature are fetched over https (redirects only
// to https) and checked against the trusted keys; the archive is checked
// against its hash, unpacked under a temporary name and renamed into place,
// and only then made current, so any failure leaves the install as it was.
// With no version named, a release that is not newer is refused.

package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mk/internal/release"
	"mk/internal/toolchain"
)

// UpgradeConfig is one mk upgrade. The zero value of each field means the
// running mk's own: its release address, its install root, its executable,
// its host and its built-in keys.
type UpgradeConfig struct {
	Version string        // the release to install; "" means the newest
	Base    string        // the release address; "" means MK_RELEASES, else the compiled-in one
	Home    string        // the install root; "" means MK_HOME, else the one the executable lives in
	Exe     string        // the running executable; "" means os.Executable
	Host    string        // release.HostWindows or HostLinux; "" means this host
	Keys    []release.Key // the trusted keys; nil means release.Trusted()
	Client  *http.Client  // nil means a client with a 30-minute timeout
	Getenv  func(string) string
	// Running is the running mk's version, the result's Old when no
	// current is installed; "" means Release. Tests inject it so that none
	// depends on the version this mk is built as.
	Running string
}

// UpgradeResult says what mk upgrade did.
type UpgradeResult struct {
	Old, New string // the versions before and after
	Dir      string // where the new version is
	// Installed is true for a script install (current moved), false for a
	// self-contained archive (unpacked beside the running one, PATH not
	// touched).
	Installed bool
	// Reused is true when the version was already unpacked.
	Reused bool
	// Unpacked is true once the new version is in Dir, even when moving
	// current then failed.
	Unpacked bool
}

// upgradeOnFile is the unpack hook (release.Extract's onFile); tests set
// it to interrupt an unpack.
var upgradeOnFile func(string) error

// Limits on what mk upgrade reads.
const (
	maxSumsBytes    = 1 << 20
	maxArchiveBytes = 4 << 30
)

func (c UpgradeConfig) defaults() (UpgradeConfig, error) {
	if c.Getenv == nil {
		c.Getenv = os.Getenv
	}
	if c.Base == "" {
		c.Base = c.Getenv("MK_RELEASES")
	}
	if c.Base == "" {
		c.Base = release.Base()
	}
	if c.Home == "" {
		c.Home = c.Getenv("MK_HOME")
	}
	if c.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return c, err
		}
		c.Exe = exe
	}
	if c.Host == "" {
		c.Host = release.HostOf(runtime.GOOS, runtime.GOARCH)
	}
	if c.Running == "" {
		c.Running = Release
	}
	if c.Keys == nil {
		c.Keys = release.Trusted()
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 30 * time.Minute}
	}
	if c.Client.CheckRedirect == nil {
		cl := *c.Client
		cl.CheckRedirect = httpsRedirect
		c.Client = &cl
	}
	return c, nil
}

// httpsRedirect follows a redirect only to https, or to plain http on a
// loopback host (the tests' httptest server), at most ten times: a release
// address never downgrades to plain http.
func httpsRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("more than 10 redirects")
	}
	if err := checkBase(req.URL.String()); err != nil {
		return fmt.Errorf("redirected: %w", err)
	}
	return nil
}

// install is the script install the config names (MK_HOME) or the
// executable lives in, ok=false for none.
func (c UpgradeConfig) install() (release.Install, bool) {
	if c.Home != "" {
		return release.Install{Root: c.Home}, true
	}
	if root, ok := release.FindInstall(toolchain.RealPath(c.Exe), c.Exe); ok {
		return release.Install{Root: root}, true
	}
	return release.Install{}, false
}

// checkBase refuses a release address that is not https, except http to a
// loopback host (the tests' httptest server).
func checkBase(base string) error {
	if base == "" {
		return errors.New("this mk has no release address (it is not a release build); set MK_RELEASES")
	}
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("bad release address %q: %v", base, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("release address %q is not https", base)
}

func (c UpgradeConfig) get(file string, limit int64) ([]byte, error) {
	u := release.FileURL(c.Base, c.Version, file)
	resp, err := c.Client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", u, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", u, limit)
	}
	return b, nil
}

// pick is the archive of the host in a verified SHA256SUMS: the version
// asked for, or the newest the file lists.
func pick(sums release.Sums, version, host string) (name, v string, err error) {
	var best release.Version
	for n := range sums {
		nv, h, ok := release.ParseArchiveName(n)
		if !ok || h != host {
			continue
		}
		pv, _ := release.ParseVersion(nv)
		if version != "" {
			if want, _ := release.ParseVersion(version); pv.Compare(want) == 0 {
				return n, nv, nil
			}
			continue
		}
		if name == "" || pv.Compare(best) > 0 {
			name, v, best = n, nv, pv
		}
	}
	if name == "" {
		what := "a release"
		if version != "" {
			what = "mk " + version
		}
		return "", "", fmt.Errorf("%s has no archive for %s in its %s", what, host, release.SumsFile)
	}
	return name, v, nil
}

// download writes the archive of the version (taken from the verified
// SHA256SUMS, so a newest-release request reads the same release's file)
// into dir under a temporary name, checks its SHA-256 against want, and
// returns the file's path. On any error the file is gone.
func (c UpgradeConfig) download(version, name, want, dir string) (string, error) {
	u := release.FileURL(c.Base, version, name)
	resp, err := c.Client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", u, resp.Status)
	}
	f, err := os.CreateTemp(dir, ".download-*-"+name)
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return "", fmt.Errorf("%s: %v", u, err)
	}
	if n > maxArchiveBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", u, int64(maxArchiveBytes))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("%s: SHA-256 %s, but the signed %s says %s", name, got, release.SumsFile, want)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	keep = true
	return f.Name(), nil
}

// Upgrade runs mk upgrade: it fetches the release's SHA256SUMS and
// SHA256SUMS.sig and checks the signature with the trusted keys, downloads
// the host's archive and checks its hash, unpacks it under a temporary
// name renamed into place, and moves current. A wrong signature, a hash
// mismatch or a refused or interrupted unpack leaves everything as it was.
// For an mk no script installed, it unpacks the new version beside the
// running one and touches nothing else.
func Upgrade(cfg UpgradeConfig) (UpgradeResult, error) {
	c, err := cfg.defaults()
	if err != nil {
		return UpgradeResult{}, err
	}
	if c.Host == "" {
		return UpgradeResult{}, fmt.Errorf("no release carries %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if c.Version != "" {
		if _, err := release.ParseVersion(c.Version); err != nil {
			return UpgradeResult{}, err
		}
	}
	if err := checkBase(c.Base); err != nil {
		return UpgradeResult{}, err
	}
	sums, err := c.get(release.SumsFile, maxSumsBytes)
	if err != nil {
		return UpgradeResult{}, err
	}
	sig, err := c.get(release.SigFile, maxSumsBytes)
	if err != nil {
		return UpgradeResult{}, err
	}
	if _, err := release.Verify(sums, sig, c.Keys); err != nil {
		return UpgradeResult{}, fmt.Errorf("%s: %w", release.SigFile, err)
	}
	listed, err := release.ParseSums(sums)
	if err != nil {
		return UpgradeResult{}, err
	}
	name, newV, err := pick(listed, c.Version, c.Host)
	if err != nil {
		return UpgradeResult{}, err
	}
	in, scripted := c.install()
	res := UpgradeResult{Old: c.Running, New: newV, Installed: scripted}
	if scripted {
		if cur, err := in.Current(); err == nil && cur != "" {
			res.Old = cur
		}
	}
	if c.Version == "" {
		if err := newer(newV, c.Running, res.Old); err != nil {
			return UpgradeResult{}, err
		}
	}
	if !scripted {
		top, err := unpackedRelease(toolchain.RealPath(c.Exe))
		if err != nil {
			return UpgradeResult{}, err
		}
		res.Dir = filepath.Join(filepath.Dir(top), release.TopDir(newV, c.Host))
		if _, err := os.Lstat(res.Dir); err == nil {
			return res, fmt.Errorf("%s already exists", res.Dir)
		}
		arc, err := c.download(newV, name, listed[name], filepath.Dir(res.Dir))
		if err != nil {
			return res, err
		}
		defer os.Remove(arc)
		if err := release.UnpackAs(arc, res.Dir, newV, c.Host, upgradeOnFile); err != nil {
			return res, err
		}
		res.Unpacked = true
		return res, nil
	}
	res.Dir = in.Dir(newV)
	if fi, err := os.Stat(res.Dir); err == nil && fi.IsDir() {
		res.Reused = true
	} else {
		if err := os.MkdirAll(in.Toolchains(), 0o755); err != nil {
			return res, err
		}
		arc, err := c.download(newV, name, listed[name], in.Toolchains())
		if err != nil {
			return res, err
		}
		defer os.Remove(arc)
		if err := in.Unpack(arc, newV, c.Host, upgradeOnFile); err != nil {
			return res, err
		}
	}
	res.Unpacked = true
	return res, in.Use(newV)
}

// newer refuses, for mk upgrade with no version named, a release that is not
// newer than the running mk and current: the newest address must never move
// an install back. Naming the version installs an older one.
func newer(newV string, olds ...string) error {
	nv, err := release.ParseVersion(newV)
	if err != nil {
		return err
	}
	for _, o := range olds {
		ov, err := release.ParseVersion(o)
		if err != nil || o == "" {
			continue
		}
		if nv.Compare(ov) <= 0 {
			return fmt.Errorf("the newest release, mk %s, is not newer than mk %s; to go back, name the version: mk upgrade %s", newV, o, newV)
		}
	}
	return nil
}

// unpackedRelease is the top directory of the unpacked release an mk not
// installed by a script runs from: exe must be <top>/bin/mk[.exe] with
// RELEASE.txt or llvm/bin in <top>. Anything else is refused, so mk upgrade
// never writes two levels above a stray mk.
func unpackedRelease(exe string) (string, error) {
	bin := filepath.Dir(exe)
	top := filepath.Dir(bin)
	if strings.EqualFold(filepath.Base(bin), "bin") {
		for _, marker := range []string{toolchain.BundleMarker, filepath.Join("llvm", "bin")} {
			if _, err := os.Stat(filepath.Join(top, marker)); err == nil {
				return top, nil
			}
		}
	}
	return "", fmt.Errorf("%s is not in an unpacked release (bin/%s beside %s or llvm/bin) nor in a script install; set MK_HOME, or unpack the release by hand",
		exe, filepath.Base(exe), toolchain.BundleMarker)
}

// ToolchainConfig names the install mk toolchain works on.
type ToolchainConfig struct {
	Home   string // "" means MK_HOME, else the install the executable lives in
	Exe    string // "" means os.Executable
	Getenv func(string) string
}

// InstalledToolchain is one row of mk toolchain list.
type InstalledToolchain struct {
	Version string
	Current bool
}

func (t ToolchainConfig) install() (release.Install, string, error) {
	c, err := UpgradeConfig{Home: t.Home, Exe: t.Exe, Getenv: t.Getenv}.defaults()
	if err != nil {
		return release.Install{}, "", err
	}
	in, ok := c.install()
	if !ok {
		return in, "", errors.New("this mk was not installed by install.ps1 or install.sh (no toolchains directory above it); set MK_HOME")
	}
	// The running toolchain's directory (bin/..), compared with the one
	// to remove by file identity.
	return in, filepath.Dir(filepath.Dir(toolchain.RealPath(c.Exe))), nil
}

// ToolchainList lists the installed versions, oldest first, marking
// current.
func ToolchainList(t ToolchainConfig) ([]InstalledToolchain, error) {
	in, _, err := t.install()
	if err != nil {
		return nil, err
	}
	vs, err := in.Versions()
	if err != nil {
		return nil, err
	}
	cur, err := in.Current()
	if err != nil {
		return nil, err
	}
	out := make([]InstalledToolchain, len(vs))
	for i, v := range vs {
		out[i] = InstalledToolchain{Version: v, Current: v == cur}
	}
	return out, nil
}

// ToolchainUse moves current to an installed version.
func ToolchainUse(t ToolchainConfig, version string) error {
	in, _, err := t.install()
	if err != nil {
		return err
	}
	return in.Use(version)
}

// ToolchainRemove removes an installed version other than current and
// other than the running mk's.
func ToolchainRemove(t ToolchainConfig, version string) error {
	in, running, err := t.install()
	if err != nil {
		return err
	}
	return in.Remove(version, running)
}

// UpgradeFailure is mk upgrade's message for err. When the new version was
// already unpacked (only moving current failed), it says where the version
// is rather than that nothing changed.
func UpgradeFailure(res UpgradeResult, err error) string {
	if res.Unpacked {
		return fmt.Sprintf("mk upgrade: mk %s is unpacked in %s, but current still points at mk %s: %v", res.New, res.Dir, res.Old, err)
	}
	return fmt.Sprintf("mk upgrade: %v; nothing was changed", err)
}
