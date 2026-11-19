// Why: every command starts from a path the user gave, and what it names
// (one file, a package, a bare directory) decides the modules, the source
// paths diagnostics print and where the cache goes.
//
// What: rootKind, rootInfo and findRoot, which classify cfg.Root, and
// pathErrText for its errors.
//
// How: a .mk file is single-file mode, a directory with a pkg.mk a package,
// any other directory holding .mk files one module; anything else is a usage
// error. The cache directory rule lives in Config.normalize and
// Session.cacheDir.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// rootKind is how the build root was given.
type rootKind uint8

const (
	rootFile    rootKind = iota + 1 // a .mk file: single-file mode
	rootPackage                     // a directory holding pkg.mk
	rootDir                         // a directory without pkg.mk: one module
	rootNone                        // mk test --std: no package, the runner alone
)

// rootInfo is the build root.
type rootInfo struct {
	kind rootKind
	// dir is the absolute package root: the file's directory in single-file
	// mode.
	dir string
	// file is the file's base name in single-file mode.
	file string
	// prefix is the SourceMap prefix of the root's files: in single-file
	// mode the file's directory as given, normalized to '/', so a
	// diagnostic cites the file by the path as given; "" otherwise, so paths
	// are package-relative.
	prefix string
	// name is the implicit bin's name: the file's base name without .mk, or
	// the directory's base name.
	name string
}

// findRoot classifies cfg.Root. Anything that is not a .mk file or a
// directory is a usage error.
func findRoot(given string) (rootInfo, error) {
	if given == "" {
		given = "."
	}
	abs, err := filepath.Abs(given)
	if err != nil {
		return rootInfo{}, usagef("cannot use root %s: %v", given, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return rootInfo{}, usagef("cannot read root %s: %v", given, pathErrText(err))
	}
	if !fi.IsDir() {
		if !strings.HasSuffix(abs, ".mk") {
			return rootInfo{}, usagef("root %s is neither a directory nor a .mk file", given)
		}
		r := rootInfo{kind: rootFile, dir: filepath.Dir(abs), file: filepath.Base(abs)}
		r.name = strings.TrimSuffix(r.file, ".mk")
		if d := filepath.ToSlash(filepath.Dir(filepath.Clean(given))); d != "." {
			r.prefix = strings.TrimSuffix(d, "/") + "/"
		}
		if r.prefix == "std/" || strings.HasPrefix(r.prefix, "std/") {
			// std's files are cited as std/<dir>/<file>.mk; a given path in
			// that namespace keeps its "./" so the two never share a source
			// path (`mk check std/core/c.mk` from a directory holding a std/
			// of its own).
			r.prefix = "./" + r.prefix
		}
		return r, nil
	}
	r := rootInfo{kind: rootDir, dir: abs, name: filepath.Base(abs)}
	if pfi, err := os.Stat(filepath.Join(abs, "pkg.mk")); err == nil && !pfi.IsDir() {
		r.kind = rootPackage
		return r, nil
	}
	// Directory mode: the directory is the one module, and a module is a
	// directory holding at least one .mk file. One without is none of the
	// three roots, so a usage error, never a module of zero files whose
	// missing-Main E0201 would have no file to stand at.
	ents, err := os.ReadDir(abs)
	if err != nil {
		return rootInfo{}, usagef("cannot read root %s: %v", given, pathErrText(err))
	}
	if !slices.ContainsFunc(ents, func(e os.DirEntry) bool {
		return e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".mk")
	}) {
		e := usagef("root %s holds no .mk file and no pkg.mk", given)
		e.NoSources = true
		return rootInfo{}, e
	}
	return r, nil
}

// pathErrText is the OS's own text of a file-system error, without the path
// Go prefixes (so no absolute path reaches a diagnostic).
func pathErrText(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}
