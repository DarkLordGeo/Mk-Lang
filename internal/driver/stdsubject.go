// Why: `mk fix --canonical --std=<module>` rewrites std's own modules, as
// mk test --std tests them, so the driver needs std modules as a subject
// whose files live on disk.
//
// What: Session.subjectMods, stdOSPath, stdModuleOf and stdSelects.
//
// How: the modules --std selects are read and written under Config.StdDir
// (MK_STD); the embedded std is never the subject.

package driver

import (
	"path/filepath"
	"strings"
)

// subjectMods are the modules mk fix rewrites: the std modules
// Config.StdTests selects, else the package's own modules (pkgMods).
func (s *Session) subjectMods() []*modNode {
	if s.cfg.StdTests != "" {
		if s.stdB == nil {
			return nil
		}
		return s.testModules()
	}
	return s.pkgMods()
}

// stdOSPath maps a std source path ("std/coll/heap.mk") to its file under
// Config.StdDir when the file belongs to a selected module and the
// overlay read it; else "".
func (s *Session) stdOSPath(path string, ov *overlay) string {
	rel, ok := strings.CutPrefix(path, "std/")
	if !ok || s.cfg.StdDir == "" || !stdSelects(s.cfg.StdTests, stdModuleOf(rel)) {
		return ""
	}
	p := filepath.Clean(filepath.Join(s.cfg.StdDir, filepath.FromSlash(rel)))
	if !ov.wasRead(p) {
		return ""
	}
	return p
}

// stdModuleOf is the module path of a std-relative file: "coll/heap.mk"
// is std.coll, "sys/linux/io.mk" std.sys.linux, "pkg.mk" std.
func stdModuleOf(rel string) string {
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if dir == "." || dir == "" {
		return "std"
	}
	return "std." + strings.ReplaceAll(dir, "/", ".")
}

// stdSelects reports whether --std=sel selects module path m: m itself,
// a module under it, or every module for "std".
func stdSelects(sel, m string) bool {
	return sel == "std" || m == sel || strings.HasPrefix(m, sel+".")
}
