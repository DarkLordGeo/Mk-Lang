// Why: std.db.sqlite's VFS runs over std.sys's Linux calls there, and the
// amalgamation must build for the Linux triple too.
// What: TestStdSqlite_BuildLinux and TestStdSqlite_Linux (notes and file
// programs in WSL, which must print the Windows output).
// How: cross-builds at dev and release; runs under MK_LINUX=1 only, inside
// the WSL lock with go test -p 1.

package std

import (
	"path/filepath"
	"testing"
)

// buildSqliteLinux builds src for x86_64-linux at dev or release and
// returns the executable's path. Building starts no wsl.exe.
func buildSqliteLinux(t *testing.T, src string, flags ...string) string {
	t.Helper()
	r := newSqliteRoot(t, src, true, "[]")
	exe := filepath.Join(r.pkg, "p")
	args := append([]string{"build", "--target=x86_64-linux"}, flags...)
	if out, st := r.mk(t, append(args, "-o", exe, ".")...); st != 0 {
		t.Fatalf("mk build x86_64-linux %v: exit %d\n%s", flags, st, out)
	}
	return exe
}

// TestStdSqlite_BuildLinux builds both programs for x86_64-linux at dev
// and release; it starts no wsl.exe, so it runs on every Windows run.
func TestStdSqlite_BuildLinux(t *testing.T) {
	for _, flags := range [][]string{nil, {"--release"}} {
		buildSqliteLinux(t, notesProgram, flags...)
		buildSqliteLinux(t, fileProgram, flags...)
	}
}

// TestStdSqlite_Linux runs the notes and file programs under WSL at dev
// and release and wants the Windows output.
func TestStdSqlite_Linux(t *testing.T) {
	needWSL(t)
	notesWant := "1 first [hello]\n2 second [wörld]\n3 third []\n4 not found\nsyntax error\nconstraint\n"
	for _, flags := range [][]string{nil, {"--release"}} {
		for _, c := range []struct{ src, want string }{{notesProgram, notesWant}, {fileProgram, fileWant}} {
			exe := buildSqliteLinux(t, c.src, flags...)
			out, stderr, code := runWSL(t, exe, nil)
			if code != 0 || out != c.want {
				t.Errorf("linux %v: exit %d\nstdout:\n%s\nstderr:\n%s\nwant:\n%s", flags, code, out, stderr, c.want)
			}
		}
	}
}
