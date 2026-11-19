// Why: std and `mk explain` texts live inside the binary, but an editor can only
// open files.
//
// What: the files the server writes under the cache directory: the read-only std
// copy (stdDisk, stdModuleDir, which follows std/pkg.mk's per-OS module
// directories) and explainURI's `mk explain` texts.
//
// How: each check's SourceMap holds every std file it loaded with its text, so the
// server writes them from there, read-only; no driver export and no import
// of mk/std is needed.

package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"mk/internal/driver"
	"mk/internal/source"
)

// stdDisk is the directory holding std's files on disk: --std when given,
// else the materialised copy ("" until the first check that loaded std).
func (s *Server) stdDisk() string {
	if s.opt.StdDir != "" {
		return s.opt.StdDir
	}
	return s.stdCopy
}

// cacheDirOf is the cache directory a root's checks use.
func (s *Server) cacheDirOf(r *root) string {
	if s.isStdRoot(r) {
		return s.stdCacheDir()
	}
	cfgRoot := r.dir
	if r.file != "" {
		cfgRoot = r.file
	}
	dir, err := driver.CacheDirFor(cfgRoot, s.opt.CacheDir)
	if err != nil {
		return ""
	}
	return dir
}

// materializeStd writes the std files of a check's SourceMap, read-only,
// under <cache>/lsp/std-<hash>/, where the hash covers every std path and
// text, so an edited MK_STD or a new embedded std gets a new directory. A
// directory already there is used as it is. Worker only.
func (s *Server) materializeStd(r *root, sm *source.SourceMap) {
	if s.opt.StdDir != "" || s.stdCopy != "" || sm == nil || s.isStdRoot(r) {
		return
	}
	var files []*source.File
	for _, f := range sm.Files() {
		if strings.HasPrefix(f.Path, "std/") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return
	}
	cache := s.cacheDirOf(r)
	if cache == "" {
		return
	}
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write([]byte(f.Text))
		h.Write([]byte{0})
	}
	dir := filepath.Join(cache, "lsp", "std-"+hex.EncodeToString(h.Sum(nil))[:16])
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		s.stdCopy = dir
		return
	}
	// Write a temporary sibling and rename it, so a second server sharing
	// the cache never sees half a tree.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "std-tmp-")
	if err != nil {
		return
	}
	for _, f := range files {
		p := filepath.Join(tmp, filepath.FromSlash(strings.TrimPrefix(f.Path, "std/")))
		if os.MkdirAll(filepath.Dir(p), 0o755) != nil || os.WriteFile(p, []byte(f.Text), 0o444) != nil {
			removeTree(tmp)
			return
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		removeTree(tmp)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return
		}
	}
	s.stdCopy = dir
	s.stdList = nil
}

// removeTree removes a tree whose files are read-only.
func removeTree(dir string) {
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// stdFiles lists std's .mk files on disk, sorted, test files last.
func (s *Server) stdFiles() []string {
	dir := s.stdDisk()
	if dir == "" {
		return nil
	}
	if s.stdList != nil && s.stdListDir == dir {
		return s.stdList
	}
	var main, tests []string
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		// A manifest is no module file, except std.pkg's own std/pkg/pkg.mk.
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".mk") || e.Name() == "pkg.mk" && filepath.Base(filepath.Dir(p)) != "pkg" {
			return nil
		}
		if strings.HasPrefix(e.Name(), "tests_") || strings.HasSuffix(e.Name(), "_test.mk") {
			tests = append(tests, p)
		} else {
			main = append(main, p)
		}
		return nil
	})
	sort.Strings(main)
	sort.Strings(tests)
	s.stdList, s.stdListDir = append(main, tests...), dir
	return s.stdList
}

// stdModuleDir is the directory of a std module path (`std.fs`), or "".
func (s *Server) stdModuleDir(mod string) string {
	rest, ok := strings.CutPrefix(mod, "std.")
	if !ok || s.stdDisk() == "" {
		return ""
	}
	dir := joinModule(s.stdDisk(), rest)
	// A module std's manifest selects per target (std.sys is sys/windows
	// here, std.linux is linux/other): this OS's directory, as std/pkg.mk
	// names it, else the one subdirectory that holds sources.
	if es, err := os.ReadDir(dir); err == nil {
		var subs []string
		for _, e := range es {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mk") {
				return dir
			}
			if e.IsDir() && hasMkFile(filepath.Join(dir, e.Name())) {
				subs = append(subs, filepath.Join(dir, e.Name()))
			}
		}
		if sub := selectedModuleDir(s.stdDisk(), rest); sub != "" && isDir(sub) {
			return sub
		}
		if sub := filepath.Join(dir, runtime.GOOS); isDir(sub) {
			return sub
		}
		if len(subs) == 1 {
			return subs[0]
		}
	}
	return dir
}

// selectModule reads one `pkg.Module(name: "linux", dir: "linux/other")` of
// a manifest's line.
var selectModule = regexp.MustCompile(`pkg\.Module\(name: "([^"]+)", dir: "([^"]+)"\)`)

// selectedModuleDir is the directory std/pkg.mk selects for module rest on
// this OS, or "" when the manifest names none.
func selectedModuleDir(std, rest string) string {
	raw, err := os.ReadFile(filepath.Join(std, "pkg.mk"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.Contains(l, `os: "`+runtime.GOOS+`"`) {
			continue
		}
		for _, m := range selectModule.FindAllStringSubmatch(l, -1) {
			if m[1] == rest {
				return filepath.Join(std, filepath.FromSlash(m[2]))
			}
		}
	}
	return ""
}

func hasMkFile(dir string) bool {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mk") {
			return true
		}
	}
	return false
}

// explainURI is a file URI holding `mk explain code`'s text, written once
// under <cache>/lsp/explain/, or "" when the code has no entry. It is the
// diagnostic's codeDescription.href.
func (s *Server) explainURI(r *root, code string) string {
	if u, ok := s.explained[code]; ok {
		return u
	}
	entry, ok := driver.Explain(code)
	if !ok {
		s.explained[code] = ""
		return ""
	}
	cache := s.cacheDirOf(r)
	if cache == "" {
		return ""
	}
	p := filepath.Join(cache, "lsp", "explain", code+".txt")
	text := entry.Text()
	if old, err := os.ReadFile(p); err != nil || string(old) != text {
		if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
			return ""
		}
		_ = os.Chmod(p, 0o644)
		if os.WriteFile(p, []byte(text), 0o444) != nil {
			return ""
		}
	}
	u := pathURI(p)
	s.explained[code] = u
	return u
}
