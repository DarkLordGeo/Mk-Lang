// Why: `become` is a keyword and StackSize has Bytes(n), so older code needs
// two mechanical rewrites; mk fix must make them exactly.
// What: tests of fix_become.go's two rounds: the rename of an identifier
// spelled become, and the `_` arm a match over StackSize gains past Default
// and Large.
// How: runs mk fix on packages against the real std (StackSize's own
// variants) and compares the rewritten text and the round's report.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/syntax"
)

const fixBecomeManifest = `const Package = pkg.Package(
    name: "app",
    bins: [pkg.Bin(name: "app", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

// TestFixBecome_RenamesIdentifiers: an older package naming a parameter, a
// local and a field become checks clean after mk fix,
// every such name spelled become_, while a become statement of the same
// file stays the keyword. A second mk fix changes nothing.
func TestFixBecome_RenamesIdentifiers(t *testing.T) {
	t.Parallel()
	src := `struct Pair {
    become: int
    b: int
}

Add(become: int, b: int): int {
    return become + b
}

Loop(n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    become Loop(n - 1, acc + 1)
}

export Main(sys: &System): WriteError!void {
    become := Add(1, Loop(3, 0))
    p := Pair(become: become, b: 2)
    n := p.become
    try sys.Print("{n}\n", (n: n))
}
`
	want := `struct Pair {
    become_: int
    b: int
}

Add(become_: int, b: int): int {
    return become_ + b
}

Loop(n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    become Loop(n - 1, acc + 1)
}

export Main(sys: &System): WriteError!void {
    become_ := Add(1, Loop(3, 0))
    p := Pair(become_: become_, b: 2)
    n := p.become_
    try sys.Print("{n}\n", (n: n))
}
`
	dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": src})
	cache := t.TempDir()
	session := func() *Session {
		s, err := NewSession(Config{Root: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if r := session().Check(); len(r.Report.Diags) == 0 {
		t.Fatal("the 1.2 source checks clean before mk fix: become is not a keyword")
	}
	res, err := session().Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix: %v %q %+v\n%s", err, res.Stopped, res.Rounds, diagLines(res.Final))
	}
	why := false
	for _, r := range res.Rounds {
		for _, a := range r.Applied {
			why = why || a.Why == becomeWhy
		}
	}
	if !why {
		t.Errorf("no round applied the rename: %+v", res.Rounds)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "src", "main.mk"))
	if string(got) != want {
		t.Errorf("after mk fix:\n%s\nwant:\n%s", got, want)
	}
	if res, err := session().Fix(FixOptions{}); err != nil || len(res.Rounds) != 0 {
		t.Errorf("second mk fix: %v %+v", err, res.Rounds)
	}
}

// TestFixBecome_StatementOrName: the round's token rule, case by case.
func TestFixBecome_StatementOrName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		src  string
		stmt bool
	}{
		{"F() {\n    become G(1)\n}\n", true},
		{"F() {\n    become self.G(1)\n}\n", true},
		{"F() {\n    become (f)(1)\n}\n", true},
		{"F() {\n    x := 1\n    become t[x](1)\n}\n", true},
		{"F() {\n    become(1)\n}\n", false},
		{"F() {\n    become := 1\n}\n", false},
		{"F() {\n    become = 1\n}\n", false},
		{"F() {\n    become.G(1)\n}\n", false},
		{"F() {\n    x := become\n}\n", false},
		{"F() {\n    x := p.become\n}\n", false},
		{"F() {\n    G(become: 1)\n}\n", false},
		{"F(become: int) {\n}\n", false},
	} {
		toks, _ := syntax.Lex(1, c.src)
		found := false
		for i, tk := range toks {
			if tk.Text(c.src) == "become" {
				found = true
				if got := becomeStatement(toks, i); got != c.stmt {
					t.Errorf("%q: statement %v, want %v", c.src, got, c.stmt)
				}
			}
		}
		if !found {
			t.Errorf("%q: no become token", c.src)
		}
	}
}

// TestFixBecome_StackSizeArm: with std's StackSize.Bytes(n) beside Default
// and Large, a statement match and a value match naming only the two older variants
// are E0306; mk fix gives each a `_` arm that panics, and the package
// checks clean. A match with a `_` arm already is left alone.
func TestFixBecome_StackSizeArm(t *testing.T) {
	t.Parallel()
	src := `Kind(s: StackSize): int {
    match s {
        .Default {
            return 1
        }
        .Large {
            return 2
        }
    }
}

Value(s: StackSize): int {
    return match s {
        .Default { 1 }
        .Large { 2 } }
}

Other(s: StackSize): int {
    return match s {
        .Default { 1 }
        _ { 3 }
    }
}

export Main(sys: &System): WriteError!void {
    n := Kind(StackSize.Large) + Value(StackSize.Default) + Other(StackSize.Large)
    try sys.Print("{n}\n", (n: n))
}
`
	want := `Kind(s: StackSize): int {
    match s {
        .Default {
            return 1
        }
        .Large {
            return 2
        }
        _ {
            Panic("StackSize gained a member this match does not handle")
        }
    }
}

Value(s: StackSize): int {
    return match s {
        .Default { 1 }
        .Large { 2 }
        _ {
            Panic("StackSize gained a member this match does not handle")
        }
    }
}

Other(s: StackSize): int {
    return match s {
        .Default { 1 }
        _ { 3 }
    }
}

export Main(sys: &System): WriteError!void {
    n := Kind(StackSize.Large) + Value(StackSize.Default) + Other(StackSize.Large)
    try sys.Print("{n}\n", (n: n))
}
`
	dir := writeTree(t, map[string]string{"pkg.mk": fixBecomeManifest, "src/main.mk": src})
	cache := t.TempDir()
	session := func() *Session {
		s, err := NewSession(Config{Root: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	r := session().Check()
	if n := strings.Count(diagLines(r), "E0306"); n != 2 || len(r.Report.Diags) != 2 {
		t.Fatalf("before mk fix, want two E0306:\n%s", diagLines(r))
	}
	res, err := session().Fix(FixOptions{})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix: %v %q %+v\n%s", err, res.Stopped, res.Rounds, diagLines(res.Final))
	}
	got, _ := os.ReadFile(filepath.Join(dir, "src", "main.mk"))
	if string(got) != want {
		t.Errorf("after mk fix:\n%s\nwant:\n%s", got, want)
	}
}
