// Why: fetched package trees are shared by every build that uses the same
// cache directory and must survive the store's garbage collection, yet a half-written or
// tampered tree must never be read.
//
// What: the package tree cache: pkgCacheDir, pkgTreeDir, cachedTree,
// storeTree, removeReadOnly, PackageCache, PackageCacheSize and
// PrunePackages.
//
// How: a source package's tree lives at <cache root>/pkg/<tree hash hex>/,
// outside the store's v1/, so the store's collection never sees it. A tree
// is written to <root>/pkg/tmp-<random>/, checked in memory first
// (Tree.WriteVerified), made read-only, then renamed into place; a
// concurrent writer of the same tree loses the rename and uses the winner's.
// A build whose pkg.sum names a tree that is cached reads it from disk,
// offline too; with MK_CACHE_VERIFY=1 (Config.CacheVerify) the tree is
// hashed again on use, and a mismatch is E0202 sum-mismatch. The cache root
// is the build's cache directory (Session.cacheDir).

package driver

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mk/internal/fetch"
)

// pkgCacheDir is the package trees' directory under cache root dir.
func pkgCacheDir(dir string) string { return filepath.Join(dir, "pkg") }

// pkgTreeDir is the directory of the tree whose hash is h.
func pkgTreeDir(dir string, h fetch.TreeHash) string {
	return filepath.Join(pkgCacheDir(dir), hex.EncodeToString(h[:]))
}

// cachedTree is the cached tree of hash h under the session's cache, or
// "" when it is not there (or --no-cache). With verify, the tree is hashed
// again; a mismatch is an error with reason sum-mismatch.
func (s *Session) cachedTree(h fetch.TreeHash) (string, error) {
	if s.cfg.NoCache {
		return "", nil
	}
	dir := pkgTreeDir(s.cacheDir(), h)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", nil
	}
	if s.cfg.CacheVerify {
		got, err := dirTreeHash(dir)
		if err != nil {
			return "", err
		}
		if got != h {
			return "", &fetch.Error{Reason: fetch.ReasonSumMismatch,
				Msg: "the cached tree " + dir + " has " + got.String() + ", but its name says " + h.String()}
		}
	}
	return dir, nil
}

// storeTree writes t, whose hash is h, into the package cache (nothing
// under --no-cache) and returns its directory. A failed write is not an
// error of the build: the tree is in memory, and the next build fetches.
func (s *Session) storeTree(t *fetch.Tree, h fetch.TreeHash) string {
	if s.cfg.NoCache {
		return ""
	}
	root := pkgCacheDir(s.cacheDir())
	final := pkgTreeDir(s.cacheDir(), h)
	if fi, err := os.Stat(final); err == nil && fi.IsDir() {
		return final
	}
	if os.MkdirAll(root, 0o755) != nil {
		return ""
	}
	tmp, err := os.MkdirTemp(root, "tmp-")
	if err != nil {
		return ""
	}
	if err := os.Remove(tmp); err != nil || t.WriteVerified(tmp, h) != nil {
		os.RemoveAll(tmp)
		return ""
	}
	_ = filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o444)
		}
		return nil
	})
	if err := os.Rename(tmp, final); err != nil {
		// Another writer won; its tree has the same hash.
		removeReadOnly(tmp)
		if fi, err := os.Stat(final); err != nil || !fi.IsDir() {
			return ""
		}
	}
	return final
}

// removeReadOnly removes dir, whose files may be read-only.
func removeReadOnly(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// PackageCache is mk cache size's package line: the trees under
// <cache>/pkg and their bytes, apart from the store's.
type PackageCache struct {
	Trees, Bytes int64
}

// PackageCacheSize counts the package trees under dir's cache.
func PackageCacheSize(dir string) (PackageCache, error) {
	var pc PackageCache
	ents, err := os.ReadDir(pkgCacheDir(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return pc, nil
	}
	if err != nil {
		return pc, err
	}
	for _, e := range ents {
		if !e.IsDir() || len(e.Name()) != 64 {
			continue
		}
		pc.Trees++
		_ = filepath.WalkDir(filepath.Join(pkgCacheDir(dir), e.Name()), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					pc.Bytes += fi.Size()
				}
			}
			return nil
		})
	}
	return pc, nil
}

// PrunePackages removes from dir's package cache every tree no pkg.sum
// under refs (each a directory searched for pkg.sum files, hidden
// directories and vendor/ skipped) names, and stale tmp- directories. It
// returns the trees removed.
func PrunePackages(dir string, refs []string) ([]string, error) {
	keep := map[string]bool{}
	for _, r := range refs {
		err := filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != r && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor") {
				return fs.SkipDir
			}
			if d.IsDir() || d.Name() != sumFile {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum, err := parsePkgSum(string(b))
			if err != nil {
				return err
			}
			for _, l := range sum.lines {
				if h, ok := fetch.ParseTreeHash(l.Hash); ok {
					keep[hex.EncodeToString(h[:])] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	ents, err := os.ReadDir(pkgCacheDir(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || keep[name] || !(len(name) == 64 || strings.HasPrefix(name, "tmp-")) {
			continue
		}
		removeReadOnly(filepath.Join(pkgCacheDir(dir), name))
		removed = append(removed, name)
	}
	return removed, nil
}
