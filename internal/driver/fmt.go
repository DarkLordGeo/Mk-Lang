// Why: mk fmt has one format and no options; what is left to decide is
// which files it touches and what happens to each, and cmd/mk should not
// import internal/format directly.
//
// What: Format, FormatPath with FmtOptions, FmtFile and FmtResult (with
// ExitStatus), and fmtFile.
//
// How: the format itself is internal/format's; this file walks the path,
// formats each .mk file, and rewrites it or, under Check, only reports it.

package driver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mk/internal/format"
)

// Format formats one file's bytes (internal/format.Source): a
// *format.SyntaxError for a file that does not parse, a *format.GuardError
// for an internal failure.
func Format(path string, text []byte) ([]byte, error) { return format.Source(path, text) }

// FmtOptions are mk fmt's.
type FmtOptions struct {
	// Check lists the files whose text would change and writes nothing
	// (mk fmt --check, -l).
	Check bool
}

// FmtFile is one file's outcome.
type FmtFile struct {
	Path    string // as given, or under the given directory, with '/' separators
	Changed bool   // the text differs from its format (written unless Check)
	// Err is a *format.SyntaxError (the file is not rewritten), a
	// *format.GuardError (internal: not rewritten), or an I/O error.
	Err error
}

// FmtResult is mk fmt over a file or a tree, files in byte order of Path.
type FmtResult struct {
	Files []FmtFile
}

// ExitStatus is 0 when every file is formatted (or was rewritten), 1 when
// one has a syntax error or, under Check, would change, and 3 on an
// internal error.
func (r *FmtResult) ExitStatus(check bool) int {
	st := 0
	for _, f := range r.Files {
		var ge *format.GuardError
		switch {
		case errors.As(f.Err, &ge):
			return 3
		case f.Err != nil, check && f.Changed:
			st = 1
		}
	}
	return st
}

// FormatPath is mk fmt: path is a .mk file or a directory, every .mk file
// under which is formatted, skipping directories whose name starts with "."
// (the cache, .git) and gen/ views (generated, E0904). A missing or
// unusable path is a *UsageError.
func FormatPath(path string, opt FmtOptions) (*FmtResult, error) {
	if path == "" {
		path = "."
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, usagef("cannot read %s: %v", path, pathErrText(err))
	}
	var files []string
	if !fi.IsDir() {
		if !strings.HasSuffix(path, ".mk") {
			return nil, usagef("%s is neither a directory nor a .mk file", path)
		}
		files = []string{path}
	} else {
		err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				n := d.Name()
				if p != path && (strings.HasPrefix(n, ".") || n == "gen") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".mk") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, usagef("cannot list %s: %v", path, pathErrText(err))
		}
	}
	res := &FmtResult{}
	for _, p := range files {
		res.Files = append(res.Files, fmtFile(p, opt))
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	return res, nil
}

func fmtFile(p string, opt FmtOptions) FmtFile {
	f := FmtFile{Path: filepath.ToSlash(p)}
	raw, err := os.ReadFile(p)
	if err != nil {
		f.Err = err
		return f
	}
	out, err := format.Source(f.Path, raw)
	if err != nil {
		f.Err = err
		return f
	}
	if string(out) == string(raw) {
		return f
	}
	f.Changed = true
	if opt.Check {
		return f
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	f.Err = os.WriteFile(p, out, mode)
	return f
}
