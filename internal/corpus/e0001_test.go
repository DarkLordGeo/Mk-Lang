// Why: a failing toolchain must surface as one E0001 with useful facts.
// What: TestE0001_Snapshot against codes/E0001.json, with a stub toolchain.
// How: a Go-built stub stands in for every tool; paths are normalized out of
// the snapshot.
//
// TestE0001_Snapshot: a unit built with a toolchain whose clang is a
// Go-built stub that exits 1 with a fixed standard error gets exactly one
// E0001 with facts tool, command and stderr, and its snapshot is
// codes/E0001.json. The build goes through driver.Session.Build, which
// reports a build E0001, as the corpus session's CheckUnit does
// (TestBuildE0001ReachesTheJudge).

package corpus

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// stubStderr is what the stub tool prints on every run but --version.
const stubStderr = "stub clang: exit 1 by design (TestE0001_Snapshot)\n"

const stubSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("clang version 21.1.8 (stub)")
		return
	}
	fmt.Fprint(os.Stderr, ` + "`" + stubStderr + "`" + `)
	os.Exit(1)
}
`

// stubTools builds the stub once and installs it as every tool discovery
// looks for, then discovers that directory.
func stubTools(t *testing.T) *toolchain.Tools {
	t.Helper()
	gobin := goCommand(t, "to build the stub clang")
	dir := t.TempDir()
	src := filepath.Join(dir, "stub.go")
	if err := os.WriteFile(src, []byte(stubSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	first := filepath.Join(bin, toolchain.NameClang+exe)
	cmd := exec.Command(gobin, "build", "-o", first, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the stub clang: %v\n%s", err, out)
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{toolchain.NameLLDLink, toolchain.NameLDLLD, toolchain.NameLLVMLib} {
		if err := os.WriteFile(filepath.Join(bin, n+exe), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := toolchain.Discover(toolchain.Options{LLVMDir: bin})
	if err != nil {
		t.Fatalf("discovering the stub directory: %v", err)
	}
	return tools
}

func TestE0001_Snapshot(t *testing.T) {
	root := repoRoot(t)
	tools := stubTools(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "E0001.mk")
	if err := os.WriteFile(prog, []byte("export Main(sys: &System): int {\n    return 0\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One target whatever the host, so the snapshot's command is the same
	// everywhere.
	s, err := driver.NewSession(driver.Config{Root: prog, Target: types.X86_64Windows, Tools: tools, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	res, paths, err := s.Build(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("a build whose clang fails wrote %v", paths)
	}
	ds := res.Report.Diags
	if len(ds) != 1 || ds[0].Diag.Code != diag.E0001 {
		t.Fatalf("want exactly one E0001, got:\n%s", res.Report.Text(res.Sources))
	}
	d := *ds[0].Diag
	facts := map[string]string{}
	var keys []string
	for _, f := range d.Facts {
		keys = append(keys, f.Key)
		facts[f.Key] = f.Value.Str
	}
	for _, k := range []string{"tool", "command", "stderr"} {
		if !slices.Contains(keys, k) {
			t.Errorf("E0001 facts %v, want %s", keys, k)
		}
	}
	if facts["tool"] != toolchain.NameClang || facts["stderr"] != stubStderr || !strings.HasPrefix(facts["command"], tools.Clang+" ") {
		t.Errorf("E0001 facts tool %q, stderr %q, command %q: want clang, the stub's stderr, and a command running %s", facts["tool"], facts["stderr"], facts["command"], tools.Clang)
	}
	got := e0001Snapshot(res.Report, res.Sources, tools.Clang)
	if strings.Contains(string(got), dir) || strings.Contains(string(got), filepath.Dir(tools.Clang)) {
		t.Errorf("the snapshot keeps a temporary path: %s", got)
	}
	file := SnapshotDir + "/E0001.json"
	want, err := readCommitted(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		want = nil
	}
	if want == nil {
		t.Logf("%s does not exist yet; this run's snapshot: %s", file, got)
	}
	checkSnapshot(t, root, SnapshotCheck{Key: "TestE0001_Snapshot", File: file, Got: got, Want: want})
}

// e0001Snapshot is the report's JSON with the command fact made
// machine-independent: the stub clang's path becomes "clang", and every
// other absolute path in the command becomes "<tmp>/" and its base name.
func e0001Snapshot(r diag.Report, sm *source.SourceMap, clang string) []byte {
	out := r
	out.Diags = slices.Clone(r.Diags)
	for i, e := range out.Diags {
		d := *e.Diag
		d.Facts = slices.Clone(d.Facts)
		for j, f := range d.Facts {
			if f.Key != "command" {
				continue
			}
			// Whole paths are replaced before the split, so a path with a
			// space (a user name in %TEMP%) is still one token.
			cmd := strings.ReplaceAll(f.Value.Str, clang, toolchain.NameClang)
			cmd = strings.ReplaceAll(cmd, filepath.Clean(os.TempDir())+string(filepath.Separator), "@TMP@")
			toks := strings.Fields(cmd)
			for k, tok := range toks {
				switch {
				case strings.Contains(tok, "@TMP@") || filepath.IsAbs(tok):
					toks[k] = "<tmp>/" + filepath.Base(tok)
				}
			}
			d.Facts[j] = diag.Field{Key: f.Key, Value: diag.TextFact(strings.Join(toks, " "))}
		}
		out.Diags[i].Diag = &d
	}
	return out.WriteJSON(sm)
}
