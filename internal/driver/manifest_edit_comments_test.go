// Why: the manifest editor behind mk install and mk remove rewrites pkg.mk
// text in place, and comments next to the edited argument are easy to lose.
// What: TestPkgCmd_AddFieldAfterTrailingComment and
// TestPkgCmd_RemoveTakesItsOwnComments, with the editText helper.
// How: runs one manifestEditor edit on a fixed pkg.mk text and compares the
// unformatted result with the expected text.

package driver

import (
	"path/filepath"
	"testing"
)

// editText applies edit to dir's pkg.mk and returns the text before
// formatting (finish's format.Source is mk fmt's, tested apart).
func editText(t *testing.T, text string, edit func(e *manifestEditor) error) string {
	t.Helper()
	dir := writeTree(t, map[string]string{"pkg.mk": text})
	e, err := openManifest(filepath.Join(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := edit(e); err != nil {
		t.Fatal(err)
	}
	return e.text
}

// mk install adds deps to a call whose last argument line ends in a
// comment: the new argument goes after that line, and the comment keeps
// its text.
func TestPkgCmd_AddFieldAfterTrailingComment(t *testing.T) {
	add := func(e *manifestEditor) error { return e.add("deps", `pkg.Dep(name: "u", path: "../u")`) }
	for _, c := range []struct{ in, want string }{
		{
			"const Package = pkg.Package(\n    name: \"app\", // its name\n    bins: [], // the end\n)\n",
			"const Package = pkg.Package(\n    name: \"app\", // its name\n    bins: [], // the end\n    deps: [pkg.Dep(name: \"u\", path: \"../u\")],\n)\n",
		},
		{
			"const Package = pkg.Package(\n    name: \"app\", // its name\n    bins: [] // the end\n)\n",
			"const Package = pkg.Package(\n    name: \"app\", // its name\n    bins: [], // the end\n    deps: [pkg.Dep(name: \"u\", path: \"../u\")],\n)\n",
		},
		{
			"const Package = pkg.Package(\n    name: \"app\",\n    bins: [],\n)\n",
			"const Package = pkg.Package(\n    name: \"app\",\n    bins: [],\n    deps: [pkg.Dep(name: \"u\", path: \"../u\")],\n)\n",
		},
	} {
		if got := editText(t, c.in, add); got != c.want {
			t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
		}
	}
}

// mk remove takes the removed element's own comments (the lines
// directly above it and the rest of its line) and leaves its neighbour's.
func TestPkgCmd_RemoveTakesItsOwnComments(t *testing.T) {
	in := "const Package = pkg.Package(\n    name: \"app\",\n    deps: [\n        // x is the first\n        pkg.Dep(name: \"a\", path: \"../x\"), // a comment on a\n        // y is the second\n        pkg.Dep(name: \"b\", path: \"../y\"), // a comment on b\n    ],\n)\n"
	rm := func(name string) func(e *manifestEditor) error {
		return func(e *manifestEditor) error {
			_, al, i, err := e.depElem(name)
			if err != nil {
				return err
			}
			return e.remove(al, i)
		}
	}
	want := "const Package = pkg.Package(\n    name: \"app\",\n    deps: [\n        // y is the second\n        pkg.Dep(name: \"b\", path: \"../y\"), // a comment on b\n    ],\n)\n"
	if got := editText(t, in, rm("a")); got != want {
		t.Errorf("remove a:\n%s\nwant:\n%s", got, want)
	}
	want = "const Package = pkg.Package(\n    name: \"app\",\n    deps: [\n        // x is the first\n        pkg.Dep(name: \"a\", path: \"../x\"), // a comment on a\n    ],\n)\n"
	if got := editText(t, in, rm("b")); got != want {
		t.Errorf("remove b:\n%s\nwant:\n%s", got, want)
	}
}
