// Why: std.proc must pass arguments and environments on Linux as os/exec does,
// and must never search the current directory.
// What: TestProc_LinuxOracle (MK_LINUX=1, under tools/wsl-lock.ps1 with go
// test -p 1).
// How: the Mk spawner and a Go probe (helper and os/exec oracle) both run the
// same cases under WSL in one directory with the same environment.

package std

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// proc3Probe is the Go program for Linux: the helper's modes, and the
// oracle, which reads a spec as the Mk spawner does and prints what
// os/exec gives in the spawner's format.
const proc3Probe = `package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

func oracle(spec, mode string) {
	data, err := os.ReadFile(spec)
	if err != nil {
		os.Exit(3)
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var c []string
		for _, w := range strings.Fields(line) {
			v, err := hex.DecodeString(w[1:])
			if err != nil {
				os.Exit(4)
			}
			c = append(c, string(v))
		}
		var cmd *exec.Cmd
		switch mode {
		case "args":
			cmd = exec.Command(c[0], c[1:]...)
		default:
			cmd = exec.Command(c[0], "env")
			if mode == "extend" {
				cmd.Env = append(os.Environ(), c[1:]...)
			} else {
				cmd.Env = append([]string{}, c[1:]...)
			}
		}
		var o bytes.Buffer
		cmd.Stdout = &o
		err := cmd.Run()
		var ee *exec.ExitError
		switch {
		case err == nil:
			fmt.Fprintf(&b, "=== 0\n%s", o.String())
		case errors.As(err, &ee):
			fmt.Fprintf(&b, "=== %d\n%s", ee.ExitCode(), o.String())
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, exec.ErrDot), errors.Is(err, os.ErrNotExist):
			b.WriteString("=== error NotFound\n")
		default:
			fmt.Fprintf(&b, "=== error %v\n", err)
		}
	}
	os.Stdout.WriteString(b.String())
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "args":
		fmt.Printf("n=%d\n", len(os.Args)-2)
		for _, a := range os.Args[2:] {
			fmt.Println(hex.EncodeToString([]byte(a)))
		}
	case "env":
		var es []string
		for _, e := range os.Environ() {
			if strings.HasPrefix(strings.ToUpper(e), "MK_P3") {
				es = append(es, e)
			}
		}
		sort.Strings(es)
		for _, e := range es {
			fmt.Println(hex.EncodeToString([]byte(e)))
		}
	case "oracle":
		oracle(os.Args[2], os.Args[3])
	}
}
`

// proc3LinuxSpec writes the cases as hex lines to dir/spec-<name>.txt: the
// file's base name.
func proc3LinuxSpec(t *testing.T, dir, name string, cases []proc3Case) string {
	t.Helper()
	var b strings.Builder
	for _, c := range cases {
		for i, w := range c {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("x" + hex.EncodeToString([]byte(w)))
		}
		b.WriteByte('\n')
	}
	base := "spec-" + name + ".txt"
	if err := os.WriteFile(filepath.Join(dir, base), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

// proc3BuildSpawner builds the spawner as buildNative does, over the std
// proc3Base names (MK_PROC3_BASE: a scratch std that compiles while other
// std files are mid-edit) with this tree's std/proc over it.
func proc3BuildSpawner(t *testing.T, flags ...string) string {
	t.Helper()
	base := proc3Base()
	if base == "." {
		return buildNative(t, nil, proc3Spawner, flags...)
	}
	fixtures := map[string]string{}
	ents, err := os.ReadDir("proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if k := "proc/" + e.Name(); strings.HasSuffix(k, ".mk") {
			b, err := os.ReadFile(filepath.FromSlash(k))
			if err != nil {
				t.Fatal(err)
			}
			fixtures[k] = string(b)
		}
	}
	root := t.TempDir()
	stdDir := proc2CopyStd(t, root, base, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(proc3Spawner), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" && !slices.Contains(flags, "x86_64-linux") {
		exe += ".exe"
	}
	args := append(append([]string{"build"}, flags...), "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// TestProc_LinuxOracle: on Linux, proc.Spawn's arguments, environment and
// PATH search equal os/exec's, case for case. The Mk spawner of
// std-proc_oracle_test.go is built for x86_64-linux and a Go probe for
// linux/amd64: the probe is both the helper the cases start (its "args" and
// "env" modes) and the oracle ("oracle <spec> <mode>"), which runs the same
// cases through os/exec. Both run under WSL in one directory on the Windows
// drive, with the same environment. The cases: the fixed adversarial argument
// lists and 200 random ones (argv, which no quoting touches, but the runtime
// copies each string), the environment extended and replaced (a name given
// twice, names differing only in case, which Linux keeps apart, an empty
// value, a value holding '='), and PATH search with an empty entry and "." in
// PATH (os/exec: ErrDot; std.proc: never the current directory, so NotFound).
func TestProc_LinuxOracle(t *testing.T) {
	linuxGate(t)
	stdioNeedWSL(t)
	spawner := proc3BuildSpawner(t, "--target", "x86_64-linux")
	dir := filepath.Dir(spawner)
	src := filepath.Join(t.TempDir(), "probe.go")
	if err := os.WriteFile(src, []byte(proc3Probe), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "p3probe")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	cmd := exec.Command(goTool, "build", "-o", probe, src)
	cmd.Dir = filepath.Dir(src)
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOFLAGS=", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build probe: %v\n%s", err, out)
	}
	// p3found is in bin, which PATH names; p3here is in the working
	// directory only, which PATH's empty entry and "." name to a shell.
	b, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "bin", "p3found"), filepath.Join(dir, "p3here")} {
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	helper := wslPath(probe)

	type run struct {
		name  string
		mode  string
		cases []proc3Case
	}
	var runs []run
	var args []proc3Case
	for _, a := range proc3ArgCases {
		args = append(args, append(proc3Case{helper, "args"}, a...))
	}
	alphabet := []string{"a", "b", " ", "\t", `"`, `\`, `'`, "$x", "ü", "%", "*", "\n", "=", "~"}
	r := rand.New(rand.NewSource(8))
	for range 200 {
		c := proc3Case{helper, "args"}
		for range r.Intn(5) {
			var w strings.Builder
			for range r.Intn(8) {
				w.WriteString(alphabet[r.Intn(len(alphabet))])
			}
			c = append(c, w.String())
		}
		args = append(args, c)
	}
	runs = append(runs, run{"args", "args", args})
	entries := [][]string{
		{},
		{"MK_P3_A=1"},
		{"MK_P3_A=1", "MK_P3_A=2"},
		{"MK_P3_PARENT=over"},
		{"mk_p3_parent=lower"},
		{"MK_P3_E="},
		{"MK_P3_X=a=b", "MK_P3_Y=ü"},
		{"MK_P3_PARENT=one", "MK_P3_PARENT=two", "MK_P3_KEEP=k2"},
	}
	for _, mode := range []string{"extend", "replace"} {
		var cs []proc3Case
		for _, e := range entries {
			cs = append(cs, append(proc3Case{helper}, e...))
		}
		runs = append(runs, run{mode, mode, cs})
	}
	runs = append(runs, run{"search", "args", []proc3Case{
		{"p3found", "args", "x"},
		{"p3here", "args"},
		{"p3probe", "args"},
		{"p3-no-such-program", "args"},
		{"./p3here", "args", "dot"},
		{"bin/p3found", "args", "rel"},
	}})
	path := wslPath(filepath.Join(dir, "bin")) + "::.:/usr/local/bin:/usr/bin:/bin"
	env := []string{"MK_P3_PARENT=parent", "MK_P3_KEEP=keep", "PATH=" + path}
	var mk, gocalls []ioCall
	for _, rn := range runs {
		spec := proc3LinuxSpec(t, dir, rn.name, rn.cases)
		mk = append(mk, ioCall{env: env, watch: 240, args: []string{spec, rn.mode}})
		gocalls = append(gocalls, ioCall{env: env, watch: 240, args: []string{"oracle", spec, rn.mode}})
	}
	got := runWSLBatch(t, spawner, mk)
	want := runWSLBatch(t, probe, gocalls)
	for i, rn := range runs {
		if got[i].code != 0 || want[i].code != 0 {
			t.Fatalf("%s: spawner exit %d (stderr %q), oracle exit %d (stderr %q)", rn.name, got[i].code, got[i].stderr, want[i].code, want[i].stderr)
		}
		proc3Diff(t, rn.name, got[i].stdout, want[i].stdout, rn.cases)
	}
	// The arguments arrived as sent (the oracle could share a fault), and
	// the current directory was never searched.
	var exp strings.Builder
	for _, c := range args {
		fmt.Fprintf(&exp, "=== 0\nn=%d\n", len(c)-2)
		for _, a := range c[2:] {
			exp.WriteString(hex.EncodeToString([]byte(a)) + "\n")
		}
	}
	proc3Diff(t, "args as sent", got[0].stdout, exp.String(), args)
	if !strings.Contains(got[3].stdout, "=== error NotFound\n=== error NotFound\n=== error NotFound\n") {
		t.Fatalf("the current directory was searched:\n%s", got[3].stdout)
	}
}
