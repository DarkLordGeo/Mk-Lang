// Why: switching toolchains repoints <root>/current, which off Windows is a
// symbolic link.
// What: MakeLink and setLink for systems other than Windows.
// How: setLink makes the new link under a temporary name and renames it over
// current, so the switch is atomic.

//go:build !windows

package release

import (
	"fmt"
	"os"
	"path/filepath"
)

// MakeLink makes link, which must not exist, a symbolic link to the
// absolute directory target.
func MakeLink(link, target string) error {
	t, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(t); err != nil || !fi.IsDir() {
		return fmt.Errorf("release: link target %s is not a directory", t)
	}
	return os.Symlink(t, link)
}

// setLink points the symbolic link at target: a new link under a
// temporary name in root, renamed over the old one (rename is atomic, so
// current is never missing).
func setLink(root, link, target string) error {
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return fmt.Errorf("release: link target %s is not a directory", target)
	}
	tmp, err := tempName(root, ".current-new-")
	if err != nil {
		return err
	}
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
