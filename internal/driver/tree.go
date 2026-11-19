// Why: a build reads several kinds of source tree (a package directory, a
// single file's directory, the embedded or MK_STD standard library, a corpus
// unit held in memory), and every build must load files the same way.
//
// What: srcTree with its listing (mkFiles, isDir), reading (readFile) and
// path forms (path, sitePath), overlays (withOverlay), and dirToImport.
//
// How: one listing and reading path over a read function. Listings are
// sorted by name as bytes, and directories are matched byte for byte, so a
// case-insensitive file system cannot make "App" stand for "app".

package driver

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// srcTree is a tree of source files. Paths inside it ("rel") are
// '/'-separated and relative to its root; the SourceMap path of a file is
// prefix + rel.
type srcTree struct {
	fsys   fs.FS  // listing and reading for disk and embedded trees; nil for memory
	osRoot string // the absolute OS directory of a disk tree, "" otherwise
	prefix string // "" (package files), "std/", "<unit name>/", a given file's directory
	// sitePrefix is what a file's package-relative path puts before rel:
	// "std/" for the standard library, "" for every other tree. A panic
	// site's file and a debug file name are sitePrefix + rel, never the
	// SourceMap path: a single file's given directory and a corpus unit's
	// name are diagnostic paths only (codegen's Env.Path and the runtime's
	// Site use sitePrefix; diagnostics keep the given path).
	sitePrefix string
	// read reads a disk file by absolute OS path (Config.ReadFile).
	read func(string) ([]byte, error)
	mem  map[string]string // memory tree: rel path to LF-normalized text
	// extra is Config.Overlay on the package's disk tree: files listed with
	// their directory's files and read from memory, a disk file of the same
	// rel path included (mk test --prop's scratch file, the repl's).
	extra map[string][]byte
	// testOnly marks a package only test_deps reach, and testNames are
	// the local names the tree's package binds through its test_deps
	// (deps_testdeps.go).
	testOnly  bool
	testNames map[string]bool
}

// withOverlay sets t's overlay files (nil keeps none).
func (t *srcTree) withOverlay(ov map[string][]byte) *srcTree {
	if len(ov) > 0 {
		t.extra = ov
	}
	return t
}

// overlayFiles merges the overlay's .mk files that a listing of dir (as
// mkFiles takes it) would hold into out, sorted, without duplicates.
func (t *srcTree) overlayFiles(out []string, dir string, shallow bool, skip func(rel, name string) bool) []string {
	added := false
	for p := range t.extra {
		if !strings.HasSuffix(p, ".mk") || slices.Contains(out, p) {
			continue
		}
		d := dirOf(p)
		if shallow && d != dir {
			continue
		}
		if !shallow && dir != "" && d != dir && !strings.HasPrefix(d, dir+"/") {
			continue
		}
		if skippedDir(d, skip) {
			continue
		}
		out, added = append(out, p), true
	}
	if added {
		slices.Sort(out)
	}
	return out
}

// diskTree is the tree of an OS directory.
func diskTree(root, prefix string, read func(string) ([]byte, error)) *srcTree {
	return &srcTree{fsys: os.DirFS(root), osRoot: root, prefix: prefix, read: read}
}

// diskTree is the tree of an OS directory, read through Config.ReadFile and
// listed through TestHooks.WrapFS when a test sets it.
func (w *world) diskTree(root, prefix string) *srcTree {
	return w.wrapTree(diskTree(root, prefix, w.cfg.ReadFile))
}

// wrapTree applies TestHooks.WrapFS to a disk or embedded tree.
func (w *world) wrapTree(t *srcTree) *srcTree {
	if w.cfg.TestHooks.WrapFS != nil && t.fsys != nil {
		t.fsys = w.cfg.TestHooks.WrapFS(t.fsys)
	}
	return t
}

// path is the SourceMap path of rel.
func (t *srcTree) path(rel string) string { return t.prefix + rel }

// sitePath is the package-relative path of rel, '/' separated.
func (t *srcTree) sitePath(rel string) string { return t.sitePrefix + rel }

// readFile reads one file of the tree.
func (t *srcTree) readFile(rel string) ([]byte, error) {
	if b, ok := t.extra[rel]; ok {
		return slices.Clone(b), nil
	}
	switch {
	case t.mem != nil:
		text, ok := t.mem[rel]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return []byte(text), nil
	case t.osRoot != "":
		return t.read(filepath.Join(t.osRoot, filepath.FromSlash(rel)))
	default:
		return fs.ReadFile(t.fsys, rel)
	}
}

// isDir reports whether rel is a directory of the tree ("" is the root),
// spelled byte for byte as the listing spells it. Each segment is looked up
// in its parent's listing, never by opening a path built from the manifest's
// text, so on a case-insensitive file system "App" is not the directory
// "app": loading matches those directories byte for byte, and a directory
// accepted here under another spelling would silently form nothing. Only the
// parents of rel are listed, so a sibling (another OS's selected directory)
// is never entered.
func (t *srcTree) isDir(rel string) bool {
	if t.mem != nil {
		if rel == "" {
			return true
		}
		for p := range t.mem {
			if strings.HasPrefix(p, rel+"/") {
				return true
			}
		}
		return false
	}
	if rel == "" {
		return true
	}
	if !fs.ValidPath(rel) {
		return false
	}
	parent := "."
	for seg := range strings.SplitSeq(rel, "/") {
		ents, err := fs.ReadDir(t.fsys, parent)
		if err != nil {
			return false
		}
		i := slices.IndexFunc(ents, func(e fs.DirEntry) bool { return e.Name() == seg })
		if i < 0 {
			return false
		}
		if parent == "." {
			parent = seg
		} else {
			parent += "/" + seg
		}
		if !ents[i].IsDir() {
			// A symbolic link to a directory is a directory, as fs.Stat
			// says; the path is now in the listing's own spelling.
			fi, err := fs.Stat(t.fsys, parent)
			if err != nil || !fi.IsDir() {
				return false
			}
		}
	}
	return true
}

// mkFiles lists every .mk file of the tree in walk order (each directory's
// entries sorted by name as bytes), skipping the directories
// skip names (by rel path) with everything under them. With shallow, only
// files directly in dir are listed.
func (t *srcTree) mkFiles(dir string, shallow bool, skip func(rel, name string) bool) ([]string, error) {
	if t.mem != nil {
		var out []string
		for p := range t.mem {
			if !strings.HasSuffix(p, ".mk") {
				continue
			}
			d := path.Dir(p)
			if d == "." {
				d = ""
			}
			if shallow && d != dir {
				continue
			}
			if !shallow && dir != "" && d != dir && !strings.HasPrefix(d, dir+"/") {
				continue
			}
			if skippedDir(d, skip) {
				continue
			}
			out = append(out, p)
		}
		slices.Sort(out)
		return out, nil
	}
	start := dir
	if start == "" {
		start = "."
	}
	var out []string
	err := fs.WalkDir(t.fsys, start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == start {
				return nil
			}
			if shallow || (skip != nil && skip(p, d.Name())) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".mk") && d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) && dir == "" {
		return t.overlayFiles(nil, dir, shallow, skip), nil
	}
	if err != nil {
		return out, err
	}
	return t.overlayFiles(out, dir, shallow, skip), nil
}

// skippedDir applies skip to every ancestor directory of d.
func skippedDir(d string, skip func(rel, name string) bool) bool {
	if skip == nil || d == "" {
		return false
	}
	parts := strings.Split(d, "/")
	for i := range parts {
		if skip(strings.Join(parts[:i+1], "/"), parts[i]) {
			return true
		}
	}
	return false
}

// dirOf is the directory part of a rel path, "" for the root.
func dirOf(rel string) string {
	d := path.Dir(rel)
	if d == "." {
		return ""
	}
	return d
}

// dirToImport turns a package-relative directory into its import path
// "src/util" is "src.util".
func dirToImport(dir string) string { return strings.ReplaceAll(dir, "/", ".") }
