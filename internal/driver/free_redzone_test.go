// Why: kernel code runs where interrupts may write below the stack pointer,
// so no freestanding function may use the red zone, std's and the support
// pack's included.
// What: TestNoRedZoneEverywhere.
// How: a freestanding build's IR, every pack and the support pack, at dev
// and release, gives every defined function noredzone, a naked entry, a
// section function and std's code included; a hosted build's gives none.

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

const redZoneProgram = `// A leaf with a 64-byte local: the shape that uses the red zone.
Leaf(x: int): int {
    var buf: [8]int = [0; 8]
    for i in 0..8 {
        buf[i] = x + i
    }
    return buf[x & 7]
}

Boot(): int section ".text.boot" {
    return Leaf(3)
}

export extern naked MkEntry() {
    unsafe "the entry calls KMain and halts" {
        asm("call {m}\n2:\nhlt\njmp 2b", m: sym KMain)
    }
}

KMain() {
    _ = Boot()
}
`

// definesAtLink builds the package files for target at prof and returns
// every define line of the IR files in the link's work directory.
func definesAtLink(t *testing.T, files map[string]string, target types.Target, prof types.Profile) []string {
	t.Helper()
	tools := needTools(t)
	dir := writeTree(t, files)
	var defs []string
	cfg := Config{Root: dir, Target: target, Profile: prof, Tools: tools, CacheDir: t.TempDir()}
	cfg.TestHooks.OnLink = func(j toolchain.LinkJob) {
		ents, _ := os.ReadDir(filepath.Dir(j.Output))
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".ll") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(filepath.Dir(j.Output), e.Name()))
			if err != nil {
				t.Error(err)
				continue
			}
			defs = append(defs, regexp.MustCompile(`(?m)^define .*$`).FindAllString(string(b), -1)...)
		}
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(t.TempDir(), "p"+exeSuffix(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		t.Fatalf("build %s %v:\n%s", target.Name, prof, r.Report.Text(r.Sources))
	}
	return defs
}

// TestNoRedZoneEverywhere: on freestanding every define carries
// noredzone, the naked MkEntry, the section function and the support pack
// among them; on a hosted target none does.
func TestNoRedZoneEverywhere(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.mk": redZoneProgram,
		"pkg.mk": manifest(map[string]string{"name": `"p"`, "bins": `[pkg.Bin(name: "p", root: ".", link_script: "", cpu: "", subsystem: "console")]`,
			"unsafe_modules": `["."]`}),
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			t.Parallel()
			defs := definesAtLink(t, files, types.X86_64Freestanding, prof)
			var sawEntry, sawLeaf, sawSupport bool
			for _, d := range defs {
				if !strings.Contains(d, " noredzone") {
					t.Errorf("no noredzone: %s", d)
				}
				sawEntry = sawEntry || strings.Contains(d, "@MkEntry(")
				sawLeaf = sawLeaf || strings.Contains(d, "Leaf(")
				sawSupport = sawSupport || strings.Contains(d, "@memcpy(")
			}
			if !sawEntry || !sawSupport || (prof == types.Dev && !sawLeaf) {
				t.Errorf("entry %t, Leaf %t, memcpy %t: a define the test means to see is missing among %d", sawEntry, sawLeaf, sawSupport, len(defs))
			}
		})
	}
	t.Run("hosted", func(t *testing.T) {
		t.Parallel()
		hosted := map[string]string{
			"main.mk": "Leaf(x: int): int {\n    return x + 1\n}\n\nexport Main(sys: &System) {\n    _ = Leaf(1)\n}\n",
			"pkg.mk":  manifest(map[string]string{"name": `"p"`, "bins": `[pkg.Bin(name: "p", root: ".", link_script: "", cpu: "", subsystem: "console")]`}),
		}
		for _, d := range definesAtLink(t, hosted, HostTarget(), types.Dev) {
			if strings.Contains(d, "noredzone") {
				t.Errorf("hosted define with noredzone: %s", d)
			}
		}
	})
}
