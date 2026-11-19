// Why: TargetInfo, Arch and Os are declared in std.target, below std.core,
// and std.core's aliases give the prelude its names; the move must be
// invisible to programs, mk apidiff and mk query.
// What: TestTargetInStdTarget, TestAPIDiffSeesThroughTheMove,
// TestAPIDiffDurationThroughItsAlias and TestQueryThroughTheAliases.
// How: the driver synthesizes Target in std.target (targetHome); mk apidiff
// --std compares the aliases as the declarations they name (stdReexport),
// while a change to the moved declaration still breaks.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

// targetMovedFiles is targetStdFiles with TargetInfo, Arch and Os moved to
// std.target, std.core aliasing them; os, when not empty, replaces Os's
// variants (a change to the moved declaration).
func targetMovedFiles(t *testing.T, os string) map[string]string {
	t.Helper()
	files := targetStdFiles(t)
	core := files["core/core.mk"]
	var moved []string
	for _, decl := range []string{
		"export struct TargetInfo {\n    export arch: Arch\n    export os: Os\n    export pointer_bytes: int\n}\n",
		"export enum Arch {\n    X64\n    Arm64\n    Riscv64\n}\n",
		"export enum Os {\n    Linux\n    Windows\n    Macos\n    Freestanding\n}\n",
	} {
		if !strings.Contains(core, decl) {
			t.Fatalf("stand-in std.core has no %q", decl)
		}
		core = strings.Replace(core, decl, "", 1)
		moved = append(moved, decl)
	}
	if os != "" {
		moved[2] = os
	}
	core = strings.Replace(core, "import std.raw\n", "import std.raw\nimport std.target\n\nexport type TargetInfo = target.TargetInfo\n\nexport type Arch = target.Arch\n\nexport type Os = target.Os\n", 1)
	files["core/core.mk"] = core
	files["target/target.mk"] = strings.Join(moved, "\n")
	return files
}

// TestTargetInStdTarget: with the declarations in std.target, Target
// is the item std.target.Target with its value for each target, Known's
// four target fields are std.target's, and a package's Target.os ==
// Os.Windows and Target.arch == Arch.Arm64 still fold.
func TestTargetInStdTarget(t *testing.T) {
	stdDir := writeTree(t, targetMovedFiles(t, ""))
	root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": pageSizeSrc}))
	for _, tc := range []struct {
		target    types.Target
		page, win string
	}{
		{types.X86_64Windows, "4096", "true"},
		{types.Aarch64Linux, "16384", "false"},
	} {
		t.Run(tc.target.Name, func(t *testing.T) {
			s, err := NewSession(Config{Root: root, StdDir: stdDir, Target: tc.target})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			clean(t, r)
			k := s.w.sem5.known
			for name, id := range map[string]types.ItemID{"Target": k.Target, "TargetInfo": k.TargetInfo, "Arch": k.Arch, "Os": k.Os} {
				if id == types.NoItem {
					t.Fatalf("KnownItems.%s is NoItem", name)
				}
				if got := s.w.items.Get(id).Key.String(); got != "std.target."+name {
					t.Errorf("KnownItems.%s is %s, want std.target.%s", name, got, name)
				}
			}
			if d, ok := s.w.items.Get(k.Target).Decl.(*types.ConstDecl); !ok || !d.HasValue {
				t.Errorf("Target has no value")
			}
			for key, want := range map[string]string{"a.Page": tc.page, "a.Win": tc.win} {
				id, ok := s.w.items.ByKey(types.KeyFromString(key))
				if !ok {
					t.Fatalf("no item %s", key)
				}
				d, ok := s.w.items.Get(id).Decl.(*types.ConstDecl)
				if !ok || !d.HasValue {
					t.Fatalf("%s has no value", key)
				}
				if got := s.w.tab.ConstString(d.Value); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}

// TestAPIDiffSeesThroughTheMove: std.core's enum and struct turned
// into aliases of std.target's identical declarations is no API change;
// a variant added to the moved Os is a breaking change of std.core.Os.
func TestAPIDiffSeesThroughTheMove(t *testing.T) {
	before := writeTree(t, targetStdFiles(t))
	after := writeTree(t, targetMovedFiles(t, ""))
	if got := stdChanges(t, before, after); len(got) != 0 {
		t.Errorf("the move alone: want no change, got %v", got)
	}
	grown := writeTree(t, targetMovedFiles(t, "export enum Os {\n    Linux\n    Windows\n    Macos\n    Freestanding\n    Plan9\n}\n"))
	got := stdChanges(t, before, grown)
	if got["std.core.Os fields"] != Breaking {
		t.Errorf("a variant added to the moved Os: want std.core.Os fields breaking, got %v", got)
	}
	for k := range got {
		if !strings.HasPrefix(k, "std.core.Os ") {
			t.Errorf("unexpected change %s", k)
		}
	}
}

// durationFiles adds a std.core Duration and std.time's alias of it to a
// stand-in std; pad, when set, gives core's Duration a second field.
func durationFiles(files map[string]string, pad bool) map[string]string {
	decl := "export struct Duration {\n    ns: i64\n}\n"
	if pad {
		decl = "export struct Duration {\n    ns: i64\n    pad: i64\n}\n"
	}
	files["core/core.mk"] += "\n" + decl
	files["time/time.mk"] = "import std.core\n\nexport type Duration = core.Duration\n"
	return files
}

// TestAPIDiffDurationThroughItsAlias: std.time's Duration, an alias of
// std.core's struct, is compared as that struct, so a field added to core's
// Duration changes std.time.Duration's size.
func TestAPIDiffDurationThroughItsAlias(t *testing.T) {
	before := writeTree(t, durationFiles(targetMovedFiles(t, ""), false))
	after := writeTree(t, durationFiles(targetMovedFiles(t, ""), true))
	got := stdChanges(t, before, after)
	if got["std.time.Duration size"] != Recompile {
		t.Errorf("a field added to core's Duration: want std.time.Duration size recompile, got %v", got)
	}
	for k := range got {
		if !strings.HasPrefix(k, "std.time.Duration ") && !strings.HasPrefix(k, "std.core.Duration ") {
			t.Errorf("unexpected change %s", k)
		}
	}
}

// t3Program uses the prelude's Os, Arch and TargetInfo:
// patterns, construction through the alias, Target's fields, and std.time's
// Duration, an alias of std.core's struct.
const t3Program = `import std.time

OsCode(o: Os): int {
    match o {
        .Linux {
            return 1
        }
        .Windows {
            return 2
        }
        .Macos {
            return 3
        }
        .Freestanding {
            return 4
        }
        .Nutshell {
            return 5
        }
    }
}

ArchOf(t: TargetInfo): Arch {
    return t.arch
}

Ns(d: time.Duration): i64 {
    return d.Nanos()
}

export Main(sys: &System) {
    t := TargetInfo(arch: Arch.X64, os: Os.Linux, pointer_bytes: 8, vector_bits: 128)
    if OsCode(t.os) == OsCode(Target.os) && ArchOf(Target) == Arch.Arm64 {
        return
    }
}
`

// TestQueryThroughTheAliases: mk query members of the prelude's Os,
// Arch and TargetInfo lists std.target's variants and fields, and mk query
// uses names every use by the prelude's alias, once per site, never by
// std.target's declaration; std.time's Duration and std.core's method keep
// their names.
func TestQueryThroughTheAliases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "q.mk"), []byte(t3Program), 0o644); err != nil {
		t.Fatal(err)
	}
	for subject, want := range map[string][]string{
		"Os":         {"Linux", "Windows", "Macos", "Freestanding", "Nutshell"},
		"Arch":       {"X64", "Arm64", "Riscv64"},
		"TargetInfo": {"export arch: Arch", "export os: Os", "export pointer_bytes: int", "export vector_bits: int"},
	} {
		a := queryOne(t, dir, "members", subject)
		if !slices.Equal(a.Answer, want) {
			t.Errorf("members %s = %q, want %q", subject, a.Answer, want)
		}
	}
	if a := queryOne(t, dir, "members", "time.Duration"); !strings.Contains(a.Text(), "Nanos(self): i64") {
		t.Errorf("members time.Duration lacks Nanos:\n%s", a.Text())
	}
	a := queryOne(t, dir, "uses")
	if a.Uses == nil {
		t.Fatalf("no uses listing:\n%s", a.Text())
	}
	got := map[string][]string{}
	for _, u := range *a.Uses {
		if strings.HasPrefix(u.Item, "std.target.") {
			t.Errorf("uses names %s %v: want the prelude's alias", u.Item, u.Sites)
		}
		if c := slices.Compact(slices.Clone(u.Sites)); len(c) != len(u.Sites) {
			t.Errorf("uses lists a site of %s twice: %v", u.Item, u.Sites)
		}
		got[u.Item] = u.Sites
	}
	for _, c := range []struct{ item, site string }{
		{"std.core.Os", "q.mk:5:9"},           // the .Linux pattern
		{"std.core.Arch", "q.mk:33:68"},       // Arch.Arm64's variant
		{"std.core.TargetInfo", "q.mk:24:14"}, // t.arch, a field read
		{"std.core.TargetInfo", "q.mk:32:10"}, // the construction, once
		{"std.time.Duration", "q.mk:27:12"},
		{"std.core.Duration:Nanos", "q.mk:28:14"},
	} {
		if !slices.Contains(got[c.item], c.site) {
			t.Errorf("uses: %s lacks %s, has %v", c.item, c.site, got[c.item])
		}
	}
}
