// Why: a generator may read a file (gen's ReadFile); the module cache must
// see that read, or a warm build keeps a stale generated module.
// What: TestFrontWorldAnswersReadFile.
// How: answers the read from the file's current text in the reader's
// closure, hashed in gen's domain, and checks hits and misses.

package driver

import (
	"testing"

	"mk/internal/source"
)

// TestFrontWorldAnswersReadFile: gen's ReadFile read (the file of a struct
// whose `///` field docs a generator printed) is answered from the current
// text of that file in the reader's closure, hashed in gen's domain, so the
// module hits when the file is unchanged and misses when only a comment
// changed. A path outside the closure is not answered.
func TestFrontWorldAnswersReadFile(t *testing.T) {
	sm := source.NewSourceMap()
	own := sm.Add("main.mk", []byte("struct Opts {\n    /// the count\n    n: int\n}\n"))
	dep := sm.Add("util/u.mk", []byte("export Id(x: int): int { return x }\n"))
	other := sm.Add("far/f.mk", []byte("export F(): int { return 1 }\n"))
	u := &modNode{path: "util", files: []*fileNode{{src: dep}}}
	m := &modNode{path: "main", files: []*fileNode{{src: own}}, deps: []*modNode{u}}
	_ = &modNode{path: "far", files: []*fileNode{{src: other}}}
	file := closureFileHash(m)

	for _, f := range []*source.File{own, dep} {
		got, ok := file(f.Path)
		want := source.HashBytes("mk.file.v1", []byte(f.Text))
		if !ok || got != want {
			t.Errorf("%s: got %v %v, want %v true", f.Path, got, ok, want)
		}
	}
	if _, ok := file(other.Path); ok {
		t.Errorf("%s is outside the closure and was answered", other.Path)
	}
	before, _ := file(own.Path)
	m.files[0].src = source.NewSourceMap().Add("main.mk", []byte("struct Opts {\n    /// the number\n    n: int\n}\n"))
	after, ok := file("main.mk")
	if !ok || after == before {
		t.Errorf("a doc-comment edit kept the file hash (ok %v)", ok)
	}
}
