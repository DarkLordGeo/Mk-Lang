// Why: a runaway generic instantiation chain must be E0708 under mk check
// and mk test too, not only when mk build monomorphises.
// What: TestInstanceWalkSkipsFailedBodies, TestCheckWalksTestRoots,
// TestWalkStdAsPackage and TestWalkLeavesLostLiteralOwners.
// How: mk check runs mono's collection over the limit roots, so it reports
// E0708 as mk build does; the cases build small trees with a runaway Nest.

package driver

import (
	"path/filepath"
	"strings"
	"testing"
)

// nest instantiates Nest at [1]T per level, without end (E0708 at the
// outermost concrete call, tests/conformance/codes/E0708.mk).
const nest = "Nest[T: Plain](x: T, n: int): int {\n    if n == 0 {\n        return 0\n    }\n    return Nest([x], n - 1)\n}\n"

// TestInstanceWalkSkipsFailedBodies: Main calls a generic F[T]
// whose body has an E0301. The walk treats the failed body as a leaf:
// exactly that E0301, exit 1, and no E0000.
func TestInstanceWalkSkipsFailedBodies(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, map[string]string{"m.mk": "F[T](x: T): int {\n    return true\n}\n\n" +
		"export Main(sys: &System): int {\n    return F(1)\n}\n"})})
	if got := codes(r); len(got) != 1 || got[0] != "E0301" || ExitStatus(r.Report) != 1 {
		t.Errorf("codes %v exit %d, want one E0301 and exit 1\n%s", got, ExitStatus(r.Report), r.Report.Text(r.Sources))
	}
}

// TestCheckWalksTestRoots: the limit roots are every non-generic
// function and test block of the package, whatever the command, so a runaway
// chain reached only from a test block is E0708 under mk check, and so is
// one started by a library function in a package with no bin. Finite
// instantiation gets nothing from the walk.
func TestCheckWalksTestRoots(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string // path:line of the E0708, "" for none
	}{
		{"test block", map[string]string{"m.mk": nest + "\ntest \"nest\" {\n    Assert(Nest(1, 3) == 0)\n}\n\nexport Main(sys: &System) {\n}\n"}, "m.mk:9:"},
		{"library", pkgFiles(map[string]string{"lib/m.mk": nest + "\nexport Start(): int {\n    return Nest(1, 3)\n}\n"}), "lib/m.mk:9:"},
		{"clean", pkgFiles(map[string]string{"lib/m.mk": "One[T: Plain](x: T): int {\n    return 1\n}\n\nexport Two(): int {\n    return One(true) + One(2)\n}\n"}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := check(t, Config{Root: writeTree(t, c.files)})
			got := lines(r)
			if c.want == "" {
				if len(got) != 0 {
					t.Errorf("got %v, want nothing\n%s", got, r.Report.Text(r.Sources))
				}
				return
			}
			if len(got) != 1 || !strings.HasPrefix(got[0], c.want) || !strings.HasSuffix(got[0], ":E0708") {
				t.Errorf("got %v, want one E0708 at %s\n%s", got, c.want, r.Report.Text(r.Sources))
			}
			// mk test reports the same site, and builds nothing.
			s, err := NewSession(Config{Root: writeTree(t, c.files), CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			tr, rep, err := s.Test(TestOptions{})
			if err != nil || rep != nil || strings.Join(lines(tr), " ") != strings.Join(got, " ") {
				t.Errorf("mk test: %v report %v err %v, mk check: %v", lines(tr), rep, err, got)
			}
		})
	}
}

// TestWalkStdAsPackage: in a corpus session std is the package, and its
// limit roots are walked like any package's, with no carve-out for std: a
// runaway chain in a std module is E0708 in std's report, and the embedded
// std walks clean, so Through stays Implemented().
func TestWalkStdAsPackage(t *testing.T) {
	files := fakeStdFiles()
	files["fs/fs.mk"] = nest + "\nexport ReadText(): int {\n    return Nest(1, 3)\n}\n"
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if err != nil {
		t.Fatal(err)
	}
	got := lines(c.Std())
	if len(got) != 1 || !strings.HasPrefix(got[0], "std/fs/fs.mk:9:") || !strings.HasSuffix(got[0], ":E0708") {
		t.Errorf("stand-in std: got %v, want one E0708 at std/fs/fs.mk:9\n%s", got, c.Std().Report.Text(c.Std().Sources))
	}
	c, err = NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Std().Report.Diags) != 0 || c.Through() != Implemented() {
		t.Errorf("embedded std: Through %v, want %v\n%s", c.Through(), Implemented(), c.Std().Report.Text(c.Std().Sources))
	}
}

// TestWalkLeavesLostLiteralOwners: a FuzzCheck finding (corpus
// a0330440db9679ea). A fn literal whose check failed has no body,
// and mono's content hash of a literal is its body's, so a body that
// encloses one at any depth is a walk leaf: each form gives exactly its
// E0805 (missing return), never an E0000 of the walk.
func TestWalkLeavesLostLiteralOwners(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"slice method", "A(xs: mut []i32) {\n    xs.Sort(fn(a: &i32, b: &i32): bool {})\n}\n", "m.mk:2:41:E0805"},
		{"fn parameter", "B(f: fn(): bool): bool {\n    return f()\n}\n\nA(): bool {\n    return B(fn(): bool {})\n}\n", "m.mk:6:26:E0805"},
		{"generic", "G[T](f: T): int {\n    return 1\n}\n\nA(): int {\n    return G(fn(): bool {})\n}\n", "m.mk:6:26:E0805"},
		{"nested literal", "B(f: fn(): bool): bool {\n    return f()\n}\n\nA(): bool {\n    return B(fn(): bool {\n        return B(fn(): bool {})\n    })\n}\n", "m.mk:7:30:E0805"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := check(t, Config{Root: filepath.Join(writeTree(t, map[string]string{"m.mk": c.src}), "m.mk")})
			if got := lines(r); len(got) != 1 || !strings.HasSuffix(got[0], "/"+c.want) {
				t.Errorf("got %q, want [%s]\n%s", got, c.want, r.Report.Text(r.Sources))
			}
		})
	}
}
