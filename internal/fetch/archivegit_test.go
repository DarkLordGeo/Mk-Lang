// Why: an archive and a git tree of the same files must be refused for the
// same reasons, or a dependency's form would change what it may contain.
// What: TestDotGitRefusedInArchives and TestSameMargins.
// How: an archive refuses a .git entry as a git tree does, and both meet the
// entry and byte limits at the same margin.

package fetch

import (
	"testing"

	"mk/internal/git"
)

func TestDotGitRefusedInArchives(t *testing.T) {
	for _, p := range []string{"p/.git/config", ".GIT/hooks/pre-commit", "a/Git~1/x", ".git"} {
		if err := CheckPath(p); reason(err) != ReasonUnsafeArchive {
			t.Errorf("CheckPath(%q): %v", p, err)
		}
		if _, err := Unpack(tarGz(t, []entry{{name: "top/" + p, body: "x"}, {name: "top/ok", body: "y"}}), Limits{}); reason(err) != ReasonUnsafeArchive {
			t.Errorf("an archive holding %s: %v", p, err)
		}
		if _, err := FromGit([]git.File{{Path: p}}, Limits{}); reason(err) != ReasonUnsafeArchive {
			t.Errorf("FromGit %s: %v", p, err)
		}
	}
	for _, p := range []string{".gitignore", ".github/x", "git/x", "x.git"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q): %v", p, err)
		}
	}
}

// TestSameMargins: a/b/c/f is four entries (f and three directories)
// and 1+7+5+3+1 bytes in both forms.
func TestSameMargins(t *testing.T) {
	files := []git.File{{Path: "a/b/c/f", Data: []byte("x")}}
	es := []entry{{name: "a/b/c/f", body: "x"}, {name: "z", body: ""}} // z keeps "a" from being a lone top
	for _, tc := range []struct {
		lim     Limits
		gitOK   bool
		archive Limits
		archOK  bool
	}{
		{Limits{MaxFiles: 4}, true, Limits{MaxFiles: 5}, true},
		{Limits{MaxFiles: 3}, false, Limits{MaxFiles: 4}, false},
		{Limits{MaxBytes: 17}, true, Limits{MaxBytes: 18}, true},
		{Limits{MaxBytes: 16}, false, Limits{MaxBytes: 17}, false},
	} {
		_, err := FromGit(files, tc.lim)
		if (err == nil) != tc.gitOK {
			t.Errorf("git %+v: %v", tc.lim, err)
		}
		// The archive holds one more entry (z, one byte of path).
		_, err = Unpack(tarGz(t, es), tc.archive)
		if (err == nil) != tc.archOK {
			t.Errorf("archive %+v: %v", tc.archive, err)
		}
	}
}
