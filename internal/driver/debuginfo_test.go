// Why: a debugger needs the PDB where the image says it is, the same bytes
// on every build, and one scope per block so that shadowed names resolve.
// What: TestDebugPlacesPDB and TestSiblingScopes, the driver's
// half of mk build --debug.
// How: a COFF --debug build puts its PDB beside the executable under the
// name the image records, an ELF one keeps .debug_info, cold and warm
// builds give the same bytes, and two locals of one name in sibling scopes
// get two lexical blocks, neither inside the other (read from the IR).

package driver

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

const debugProgram = `Pick(c: bool): int {
    var r := 0
    if c {
        y := 1
        r = y
    } else {
        y := 2
        r = y + 1
    }
    return r
}

export Main(sys: &System) {
    _ = Pick(true)
}
`

// debugBuild builds main.mk of dir for target with --debug into out, with
// the store cache, and returns the executable's path.
func debugBuild(t *testing.T, dir, cache string, target types.Target, out string) string {
	t.Helper()
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), Target: target, Debug: true, Tools: needTools(t), CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("--debug build for %s:\n%s", target.Name, r.Report.Text(r.Sources))
	}
	return paths[0]
}

func sameFile(t *testing.T, a, b string) {
	t.Helper()
	x, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(x, y) {
		t.Errorf("%s and %s differ (%d and %d bytes): cold and warm --debug builds must agree", a, b, len(x), len(y))
	}
}

// TestDebugPlacesPDB: Windows: the PDB is beside the executable, named
// as the image's CodeView record names it, and cold and warm builds give
// identical executables and PDBs; Linux (linked here, not run): the ELF has
// .debug_info, and cold equals warm.
func TestDebugPlacesPDB(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{"main.mk": debugProgram})
	t.Run("coff", func(t *testing.T) {
		t.Parallel()
		cache := t.TempDir()
		cold := debugBuild(t, dir, cache, types.X86_64Windows, filepath.Join(t.TempDir(), "cold", "hello.exe"))
		warm := debugBuild(t, dir, cache, types.X86_64Windows, filepath.Join(t.TempDir(), "warm", "hello.exe"))
		var pdbs [2]string
		for i, exe := range []string{cold, warm} {
			m, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*.pdb"))
			if len(m) != 1 {
				t.Fatalf("PDBs beside %s: %q, want one", exe, m)
			}
			b, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(b, append([]byte(filepath.Base(m[0])), 0)) {
				t.Errorf("%s does not record the PDB name %s", exe, filepath.Base(m[0]))
			}
			pdbs[i] = m[0]
		}
		sameFile(t, cold, warm)
		sameFile(t, pdbs[0], pdbs[1])
	})
	t.Run("elf", func(t *testing.T) {
		t.Parallel()
		cache := t.TempDir()
		cold := debugBuild(t, dir, cache, types.X86_64Linux, filepath.Join(t.TempDir(), "cold", "hello"))
		warm := debugBuild(t, dir, cache, types.X86_64Linux, filepath.Join(t.TempDir(), "warm", "hello"))
		f, err := elf.Open(cold)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if f.Section(".debug_info") == nil {
			t.Error("no .debug_info in the --debug ELF")
		}
		if m, _ := filepath.Glob(filepath.Join(filepath.Dir(cold), "*.pdb")); len(m) != 0 {
			t.Errorf("an ELF build placed PDBs: %q", m)
		}
		sameFile(t, cold, warm)
	})
}

const scopeProgram = `enum Shape {
    Circle(y: int)
    Square(y: int)
}

IfElse(c: bool): int {
    var r := 0
    if c {
        y := 1
        r = y
    } else {
        y := 2
        r = y + 1
    }
    return r
}

Arms(s: Shape): int {
    match s {
        .Circle(y) {
            return y
        }
        .Square(y) {
            return y * 2
        }
    }
}

Loop(c: bool): int {
    var r := 0
    for {
        y := 1
        r += y
        if c {
            break
        }
    }
    y := 2
    return r + y
}

export Main(sys: &System) {
    _ = IfElse(true)
    _ = Arms(Shape.Circle(y: 3))
    _ = Loop(true)
}
`

// TestSiblingScopes: in each of IfElse, Arms and Loop the two locals
// named y are DILocalVariables of two different scopes, and neither scope is
// inside the other.
func TestSiblingScopes(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{"main.mk": scopeProgram})
	var mods []string
	cfg := Config{Root: filepath.Join(dir, "main.mk"), Debug: true, Tools: needTools(t), CacheDir: t.TempDir()}
	cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
		ents, _ := os.ReadDir(filepath.Dir(j.Output))
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".ll") {
				b, _ := os.ReadFile(filepath.Join(filepath.Dir(j.Output), e.Name()))
				mods = append(mods, string(b))
			}
		}
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(t.TempDir(), "p"+exeSuffix(HostTarget())))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	// Each pack numbers its metadata itself, so the scope graph is read per
	// module (one .ll file each).
	checked := map[string]bool{}
	for _, mod := range mods {
		parent := map[string]string{}
		for _, m := range regexp.MustCompile(`(?m)^(!\d+) = distinct !DILexicalBlock\(scope: (!\d+)`).FindAllStringSubmatch(mod, -1) {
			parent[m[1]] = m[2]
		}
		sub := map[string]string{}
		for _, m := range regexp.MustCompile(`(?m)^(!\d+) = distinct !DISubprogram\(name: "([^"]+)"`).FindAllStringSubmatch(mod, -1) {
			sub[m[1]] = m[2]
		}
		root := func(s string) string {
			for parent[s] != "" {
				s = parent[s]
			}
			return s
		}
		inside := func(a, b string) bool { // a inside b, or the same
			for ; a != ""; a = parent[a] {
				if a == b {
					return true
				}
			}
			return false
		}
		scopes := map[string][]string{}
		for _, m := range regexp.MustCompile(`(?m)^!\d+ = (?:distinct )?!DILocalVariable\(name: "y", scope: (!\d+)`).FindAllStringSubmatch(mod, -1) {
			fn := sub[root(m[1])]
			scopes[fn] = append(scopes[fn], m[1])
		}
		for fn, ss := range scopes {
			if fn != "IfElse" && fn != "Arms" && fn != "Loop" {
				continue
			}
			checked[fn] = true
			if len(ss) != 2 {
				t.Errorf("%s: %d locals named y (scopes %v), want 2", fn, len(ss), ss)
				continue
			}
			if inside(ss[0], ss[1]) || inside(ss[1], ss[0]) {
				t.Errorf("%s: the two y have scopes %s and %s, one inside the other", fn, ss[0], ss[1])
			}
		}
	}
	for _, fn := range []string{"IfElse", "Arms", "Loop"} {
		if !checked[fn] {
			t.Errorf("no local named y found in %s's debug info", fn)
		}
	}
}
