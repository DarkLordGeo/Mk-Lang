// Why: several toolchain versions live side by side, and switching between
// them must be one atomic change that never leaves a broken install.
//
// What: Install (Toolchains, CurrentLink, Dir, Current, Versions, Use),
// FindInstall, VersionOf and UnpackAs.
//
// How: the layout a script install leaves is <root>/toolchains/<version>/
// with <root>/current a junction (Windows) or a symbolic link (elsewhere) to
// one of them; mk upgrade and mk toolchain run their operations on it.
// Archives unpack under a temporary name and are renamed into place whole.

package release

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Install is a script install's root: %LOCALAPPDATA%\mk or
// ~/.local/share/mk, or MK_HOME.
type Install struct{ Root string }

// Toolchains is <root>/toolchains.
func (in Install) Toolchains() string { return filepath.Join(in.Root, "toolchains") }

// CurrentLink is <root>/current.
func (in Install) CurrentLink() string { return filepath.Join(in.Root, "current") }

// Dir is <root>/toolchains/<version>.
func (in Install) Dir(version string) string { return filepath.Join(in.Toolchains(), version) }

// ErrNotInstalled is a version with no directory under toolchains.
var ErrNotInstalled = errors.New("not installed")

// FindInstall is the install root an executable lives in: for a path
// <root>/toolchains/<version>/bin/<exe>, or <root>/current/bin/<exe> with
// <root>/toolchains present, the root. ok is false for an mk that no
// script installed (a self-contained archive, a development build).
func FindInstall(exes ...string) (root string, ok bool) {
	for _, exe := range exes {
		if exe == "" {
			continue
		}
		bin := filepath.Dir(exe)
		if !strings.EqualFold(filepath.Base(bin), "bin") {
			continue
		}
		top := filepath.Dir(bin)
		parent := filepath.Dir(top)
		if strings.EqualFold(filepath.Base(parent), "toolchains") {
			if _, err := ParseVersion(filepath.Base(top)); err == nil {
				return filepath.Dir(parent), true
			}
		}
		if strings.EqualFold(filepath.Base(top), "current") {
			if fi, err := os.Stat(filepath.Join(parent, "toolchains")); err == nil && fi.IsDir() {
				return parent, true
			}
		}
	}
	return "", false
}

// VersionOf is the installed version an executable path lives in, or "".
func (in Install) VersionOf(exe string) string {
	top := filepath.Dir(filepath.Dir(exe))
	if samePath(filepath.Dir(top), in.Toolchains()) {
		return filepath.Base(top)
	}
	return ""
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Current is the version current points at, "" when there is no current
// link or it points outside toolchains.
func (in Install) Current() (string, error) {
	target, err := ReadLink(in.CurrentLink())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(in.Root, target)
	}
	if !samePath(filepath.Dir(target), in.Toolchains()) {
		return "", nil
	}
	return filepath.Base(target), nil
}

// Versions lists the installed versions, oldest first: the directories of
// toolchains whose names are versions. Temporary unpack directories (whose
// names start with '.') are not versions.
func (in Install) Versions() ([]string, error) {
	des, err := os.ReadDir(in.Toolchains())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type vv struct {
		s string
		v Version
	}
	var vs []vv
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		if v, err := ParseVersion(de.Name()); err == nil && !strings.HasPrefix(de.Name(), "v") {
			vs = append(vs, vv{de.Name(), v})
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].v.Compare(vs[j].v) < 0 })
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.s
	}
	return out, nil
}

// installed checks that version is exactly the name of an installed
// version: its canonical spelling (ParseVersion's String, no leading 'v')
// and a name Versions lists. On Windows a case alias or a trailing dot
// names the same directory, so nothing is looked up by any other spelling.
func (in Install) installed(version string) error {
	v, err := ParseVersion(version)
	if err != nil {
		return err
	}
	if v.String() != version {
		return fmt.Errorf("release: %q is not a version's exact name (want %s)", version, v)
	}
	vs, err := in.Versions()
	if err != nil {
		return err
	}
	for _, x := range vs {
		if x == version {
			return nil
		}
	}
	return fmt.Errorf("mk %s: %w (no %s)", version, ErrNotInstalled, in.Dir(version))
}

// Use points current at an installed version. An existing current link is
// retargeted in place (setLink), so current never goes missing; one is
// made only when there is none. Stray .current-* links a crash of an
// earlier mk left in the root are removed first.
func (in Install) Use(version string) error {
	if err := in.installed(version); err != nil {
		return err
	}
	dir, err := filepath.Abs(in.Dir(version))
	if err != nil {
		return err
	}
	in.cleanStray()
	return setLink(in.Root, in.CurrentLink(), dir)
}

// cleanStray removes the links (and empty directories) named .current-*
// in the root, never what a link points at.
func (in Install) cleanStray() {
	des, err := os.ReadDir(in.Root)
	if err != nil {
		return
	}
	for _, de := range des {
		if !strings.HasPrefix(de.Name(), ".current-") {
			continue
		}
		p := filepath.Join(in.Root, de.Name())
		if RemoveLink(p) != nil {
			os.Remove(p) // an empty directory only
		}
	}
}

// Remove deletes an installed version other than current and other than
// running, the directory of the toolchain the running mk lives in ("" for
// none). Both are compared by file identity (os.SameFile), never by text.
func (in Install) Remove(version, running string) error {
	if err := in.installed(version); err != nil {
		return err
	}
	dir := in.Dir(version)
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("mk %s: %w", version, ErrNotInstalled)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if cfi, err := os.Stat(in.CurrentLink()); err == nil && os.SameFile(fi, cfi) {
		return fmt.Errorf("mk %s is current; run mk toolchain use <version> first", version)
	}
	if running != "" {
		if rfi, err := os.Stat(running); err == nil && os.SameFile(fi, rfi) {
			return fmt.Errorf("mk %s is the running mk", version)
		}
	}
	return os.RemoveAll(dir)
}

// Unpack installs a verified archive as toolchains/<version>: it unpacks
// under a temporary name inside toolchains and renames the tree into place
// only once it is whole and holds bin/mk, so an interrupted or refused
// unpack leaves no toolchains/<version> and current unchanged. onFile is
// Extract's hook.
func (in Install) Unpack(archive, version, host string, onFile func(string) error) error {
	if err := os.MkdirAll(in.Toolchains(), 0o755); err != nil {
		return err
	}
	return UnpackAs(archive, in.Dir(version), version, host, onFile)
}

// UnpackAs unpacks a verified archive to dest, which must not exist, under
// a temporary name beside it renamed into place when whole (also in the
// self-contained case, beside the running mk).
func UnpackAs(archive, dest, version, host string, onFile func(string) error) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	work, err := os.MkdirTemp(filepath.Dir(dest), ".unpack-"+version+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	tree := filepath.Join(work, "t")
	if err := Extract(archive, tree, TopDir(version, host), Limits{}, onFile); err != nil {
		return err
	}
	exe := "mk"
	if host == HostWindows {
		exe += ".exe"
	}
	if fi, err := os.Stat(filepath.Join(tree, "bin", exe)); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: the archive holds no bin/%s", ErrUnsafe, exe)
	}
	return os.Rename(tree, dest)
}

// tempName is a fresh unused path in dir starting with prefix.
func tempName(dir, prefix string) (string, error) {
	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	return name, os.Remove(name)
}

// ReadLink is the target of a symbolic link or a junction.
func ReadLink(p string) (string, error) { return os.Readlink(p) }

// RemoveLink removes a link or junction and never what it points at.
func RemoveLink(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		return fmt.Errorf("%s is not a link", p)
	}
	return os.Remove(p)
}
