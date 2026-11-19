// Why: Mk's Linux code generation and runtime need their own numbers
// against C (tests/bench/README.md "Linux"), measured on x86_64-linux inside
// WSL 2 from the Windows machine.
//
// What: publishLinux (the `publish-linux` subcommand), linuxColumns (mk,
// mk-wrap and c), linuxColumnFacts and wslPath.
//
// How: `publish-linux` runs on Windows under tools/wsl-lock.ps1 and holds
// the machine lock for the whole run: it cross-builds the Mk columns with
// the mk being measured (--target x86_64-linux), builds tools/bench for
// Linux with its peak-memory spawner tools/bench/peakspawn beside it, and
// makes exactly one wsl.exe invocation, `taskset -c 2 benchtool publish
// -platform linux ...`, which builds the C column with clang 21 inside WSL,
// checks every checksum, times the cells with hyperfine and writes
// results/<date>/linux.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type linuxOptions struct {
	Root, Work, Mk string
	Rows           []string
	Table          string
	LockPath       string
	Date           string
	LinuxWork      string // the work directory inside WSL
	Log            func(string)
}

// linuxColumns are the columns of the Linux table: Mk against C.
var linuxColumns = []string{"mk", "mk-wrap", "c"}

// linuxColumnFacts is the column table with the Linux toolchain's facts in
// place of the Windows ones (linker, CRT, prebuilt library).
func linuxColumnFacts(cs []column) []column {
	out := append([]column(nil), cs...)
	for i, c := range out {
		switch c.Lang {
		case "mk", "mk-wrap":
			out[i].Linker = "ld.lld (LLVM 21 on Windows, static ELF)"
			out[i].CRT = "none (Mk runtime, Linux system calls)"
		case "c":
			out[i].Linker = "ld.lld 21 (-fuse-ld=lld), static"
			out[i].CRT = "static glibc 2.39 (-static, -lm)"
			out[i].StdV2 = "no (prebuilt glibc)"
		}
	}
	return out
}

func publishLinux(o linuxOptions) error {
	if err := refuseExperiment(o.Mk, ""); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return errors.New("publish-linux runs on Windows and starts the Linux half inside WSL")
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" {
		return errors.New("publish-linux starts wsl.exe: run it as pwsh -NoProfile -File tools/wsl-lock.ps1 <benchtool> publish-linux ...")
	}
	if strings.HasPrefix(o.LinuxWork, "/mnt/") || !strings.HasPrefix(o.LinuxWork, "/") {
		return fmt.Errorf("-linux-work %q must be an absolute path on the Linux file system, not under /mnt", o.LinuxWork)
	}
	all, err := discover(o.Root)
	if err != nil {
		return err
	}
	table := o.Table
	if table == "" && len(o.Rows) == 0 {
		table = "same"
	}
	sel, err := selectRows(all, table, o.Rows)
	if err != nil {
		return err
	}
	var rows []*row
	var ids []string
	for _, r := range sel {
		if r.Sources["mk"] != "" && r.Sources["c"] != "" {
			rows = append(rows, r)
			ids = append(ids, r.id())
		}
	}
	if len(rows) == 0 {
		return errors.New("no row with both an Mk and a C source")
	}
	lock, err := acquireLock(o.LockPath, o.Log)
	if err != nil {
		return err
	}
	defer lock.Release()
	o.Log("holding the machine lock " + lock.Path())

	t, err := findToolchain(o.Work, o.Mk)
	if err != nil {
		return err
	}
	t.MkTarget = "x86_64-linux"
	for _, b := range buildAll(t, "linux", rows, func(c column) bool { return !c.Budget && (c.Lang == "mk" || c.Lang == "mk-wrap") }, o.Log) {
		if b.Err != nil {
			// The run inside WSL finds no program to copy and excludes the
			// cell with the reason.
			o.Log(fmt.Sprintf("cross-build %s %s failed; the cell will be excluded", b.Row.id(), b.Col.ID))
		}
	}
	tool := filepath.Join(o.Work, "linux", "benchtool")
	gb := exec.Command(t.Go, "build", "-trimpath", "-o", tool, "./tools/bench")
	gb.Dir = o.Root
	gb.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := gb.CombinedOutput(); err != nil {
		return fmt.Errorf("building tools/bench for Linux: %v\n%s", err, firstLines(string(out), 10))
	}
	// The minimal peak-memory spawner, beside benchtool and with its flags,
	// where benchtool looks for it (mem_linux.go).
	gs := exec.Command(t.Go, "build", "-trimpath", "-o", filepath.Join(filepath.Dir(tool), minSpawnerName), "./tools/bench/peakspawn")
	gs.Dir, gs.Env = o.Root, gb.Env
	if out, err := gs.CombinedOutput(); err != nil {
		return fmt.Errorf("building tools/bench/peakspawn for Linux: %v\n%s", err, firstLines(string(out), 10))
	}
	args := []string{"--cd", o.Root, "-e", "taskset", "-c", "2", wslPath(tool), "publish",
		"-platform", "linux", "-no-lock", "-prebuilt", wslPath(filepath.Join(o.Work, "bin", "linux")),
		"-cols", strings.Join(linuxColumns, ","), "-mk-label", o.Mk, "-mk-sha", fileSHA(o.Mk),
		"-mk-version", mkIdentity(o.Mk, t.version("mk", o.Mk)),
		"-date", o.Date, "-work", o.LinuxWork}
	args = append(args, ids...)
	o.Log("wsl.exe " + strings.Join(args, " "))
	cmd := exec.Command("wsl.exe", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the run inside WSL failed: %w", err)
	}
	return writeResultsMD(o.Root, o.Date)
}

// wslPath is a Windows path as WSL mounts it: V:\a\b is /mnt/v/a/b.
func wslPath(p string) string {
	vol := filepath.VolumeName(p)
	if len(vol) == 2 && vol[1] == ':' {
		return "/mnt/" + strings.ToLower(vol[:1]) + filepath.ToSlash(p[2:])
	}
	return filepath.ToSlash(p)
}
