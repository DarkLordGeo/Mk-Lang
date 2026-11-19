// Why: published benchmark rows must compare like with like, and the
// std-rewrite records must match the tree.
// What: checks of the tools/bench rows and records under
// tests/bench-lanes/std-rewrite, including TestSRW_LinesAfterMatchesTree.
// How: identical inputs and checksums, the best standard-library peer in
// every column, and each file's line count before and after.

package std

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func srwLane(parts ...string) string {
	return filepath.Join(append([]string{"..", "tests", "bench-lanes", "std-rewrite"}, parts...)...)
}

func srwGo(t *testing.T) string {
	t.Helper()
	g := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		g += ".exe"
	}
	if _, err := os.Stat(g); err != nil {
		p, err := exec.LookPath("go")
		if err != nil {
			t.Fatal("no go command")
		}
		g = p
	}
	return g
}

// srwRunN runs exe with args n times under env and fails on any output
// other than want, reporting each distinct wrong output with its count.
func srwRunN(t *testing.T, name, exe string, env []string, want string, n int, args ...string) {
	t.Helper()
	bad := map[string]int{}
	for range n {
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		got := strings.TrimSpace(string(out))
		if err != nil {
			got += " (" + err.Error() + ")"
		}
		if got != want {
			bad[got]++
		}
	}
	for got, k := range bad {
		t.Errorf("%s %s: %d of %d runs printed %q, want %s", name, strings.Join(args, " "), k, n, got, want)
	}
}

// TestSRW_SelpoolDeterministic: both cells of std-rewrite/selpool are
// the same program, whose checksum does not depend on the schedule. Before
// the fix the Go cell closed quit beside jobs, and Go's select picks at
// random among ready cases, so a worker could take quit while jobs held
// values and drop them: `1 64 64` (one worker, the buffer filled before it
// runs, checksum 2016) printed 0 in 18 of 20 runs on one machine.
// Each cell runs 20 times at `1 64 64` and 5 times at the row's quick args,
// with 8 threads (GOMAXPROCS, MK_CARRIERS) as the row's concurrency spec.
func TestSRW_SelpoolDeterministic(t *testing.T) {
	dir := t.TempDir()
	var row struct {
		Quick         []string `json:"quick"`
		QuickChecksum string   `json:"quick_checksum"`
	}
	b, err := os.ReadFile(srwLane("selpool", "bench.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &row); err != nil {
		t.Fatal(err)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	goExe := filepath.Join(dir, "selpool-go"+exe)
	cmd := exec.Command(srwGo(t), "build", "-o", goExe, srwLane("selpool", "testdata", "selpool.go"))
	cmd.Env = append(os.Environ(), "GOFLAGS=", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build selpool.go: %v\n%s", err, out)
	}
	srwRunN(t, "go", goExe, []string{"GOMAXPROCS=8"}, "2016", 20, "1", "64", "64")
	srwRunN(t, "go", goExe, []string{"GOMAXPROCS=8"}, row.QuickChecksum, 5, row.Quick...)

	mk := mkExe(t)
	mkOut := filepath.Join(dir, "selpool-mk"+exe)
	cmd = exec.Command(mk, "build", "--release", "--pgo=off", "--no-cache", "-o", mkOut, srwLane("selpool", "mk"))
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "no usable LLVM") {
			t.Skipf("the Mk cell needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build selpool: %v\n%s", err, out)
	}
	srwRunN(t, "mk", mkOut, []string{"MK_CARRIERS=8"}, "2016", 20, "1", "64", "64")
	srwRunN(t, "mk", mkOut, []string{"MK_CARRIERS=8"}, row.QuickChecksum, 5, row.Quick...)
}

// TestSRW_ChanRowsHaveZigCell: the contended channel rows compare with
// Zig's std channel, std.Io.Queue (Zig 0.17.0: many producers, many
// consumers, close, blocking putOne and getOne), so neither may skip Zig as
// having no channel; selpool may skip it only for the reason that holds
// (Zig's Select is over futures, not queues).
func TestSRW_ChanRowsHaveZigCell(t *testing.T) {
	type rowT struct {
		Sources     map[string]string `json:"sources"`
		Skip        map[string]string `json:"skip"`
		Concurrency struct {
			Models map[string]string `json:"models"`
		} `json:"concurrency"`
	}
	read := func(name string) rowT {
		b, err := os.ReadFile(srwLane(name, "bench.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r rowT
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return r
	}
	for _, name := range []string{"chan8", "chan8x0"} {
		r := read(name)
		src := r.Sources["zig"]
		if src == "" || r.Skip["zig"] != "" || r.Concurrency.Models["zig"] == "" {
			t.Errorf("%s: zig source %q, skip %q, model %q", name, src, r.Skip["zig"], r.Concurrency.Models["zig"])
			continue
		}
		b, err := os.ReadFile(srwLane(name, src))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []string{"std.Io.Queue(i64)", "putOne", "getOne", "close("} {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s/%s does not use %s", name, src, w)
			}
		}
	}
	if s := read("selpool").Skip["zig"]; s == "" || strings.Contains(s, "no std channel") {
		t.Errorf("selpool's zig skip %q", s)
	}
}

// TestSRW_LinesAfterMatchesTree: every row of lines-after.txt is the
// tree's `wc -l` (the newline count) of its file, and every .mk file of the
// write set has a row. Before, two rows patched by hand (sync_mutex.mk 581,
// sync_chan.mk 723) and one more (mem_arena.mk 696) had drifted from the
// tree (583, 725, 695). Run only with MK_SRW_LINES=1: other changes to
// files of the write set (std/task, std/rt) do not regenerate the record,
// so the gate does not run it; run it after changing the record, with
// `python -I tests/bench-lanes/std-rewrite/files_table.py.txt` to
// regenerate and `--check` for the same comparison.
func TestSRW_LinesAfterMatchesTree(t *testing.T) {
	if os.Getenv("MK_SRW_LINES") != "1" {
		t.Skip("std-rewrite's record check: set MK_SRW_LINES=1")
	}
	dirs := []string{"core", "fs", "net", "proc", "time", "sync", "atomic", "math", "derive", "rt", "task",
		"sys", "corpus", "coll", "hash", "mem", "cli", "log"}
	now := map[string]int{}
	for _, d := range dirs {
		err := filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() && e.Name() == ".mk-cache" {
				return filepath.SkipDir
			}
			if e.IsDir() || !strings.HasSuffix(p, ".mk") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			now["std/"+filepath.ToSlash(p)] = strings.Count(string(b), "\n")
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	f, err := os.Open(srwLane("lines-after.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rowRe := regexp.MustCompile(`^\s*(\d+)\s+(std/\S+\.mk)$`)
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := rowRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		seen[m[2]] = true
		got, ok := now[m[2]]
		if !ok {
			t.Errorf("%s: a row (%d), but no such file", m[2], n)
		} else if got != n {
			t.Errorf("%s: lines-after.txt %d, tree %d", m[2], n, got)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for p, n := range now {
		if !seen[p] {
			t.Errorf("%s: in the tree (%d), no row in lines-after.txt", p, n)
		}
	}
	if t.Failed() {
		t.Log("regenerate: python -I tests/bench-lanes/std-rewrite/files_table.py.txt (from the repository root)")
	}
}
