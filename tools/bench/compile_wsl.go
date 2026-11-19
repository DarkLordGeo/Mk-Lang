// Why: one compile budget is stated for ELF output ("dev build, one-function
// edit, ELF": 200 ms at 100 KLOC), so the compile table's Mk builds must also
// run on Linux, inside WSL 2 (tests/bench/README.md "Compile speed").
//
// What: `compile-linux`: compileLinux, compileLinuxArgs, linuxCompileMk and
// refuseELFExperiment (an mk with an experiment baked in is never timed).
// The results go to results/<date>/compile-linux, rendered after the
// Windows compile table.
//
// How: compile-linux runs on Windows under tools/wsl-lock.ps1, holds the
// machine lock for the whole run (as publish-linux does), builds tools/bench
// for Linux, and makes exactly one wsl.exe invocation, `taskset -c <8 CPUs>
// benchtool compile -platform linux ...`, which copies the Linux mk to the
// Linux file system and runs compileTable there with Mk only.

package main

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// linuxCompileCPUs is the taskset list of the Linux compile run: 8 virtual
// CPUs for the 8 jobs. WSL 2's numbering of virtual CPUs need not pair SMT
// siblings as Windows does, so this is 8 CPUs, not 8 cores; the table says so.
const linuxCompileCPUs = "0,2,4,6,8,10,12,14"

type compileLinuxOptions struct {
	Root, Work                               string
	Mk                                       string // the Linux (ELF) mk executable to measure
	LinuxWork                                string // the work directory inside WSL
	Spec                                     genSpec
	CleanRuns, EditRuns, WarmRuns, CheckRuns int
	LockPath                                 string
	Date                                     string
	Log                                      func(string)
}

// isELF reports whether the file at p starts with the ELF magic.
func isELF(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	n, _ := f.Read(b)
	return n == 4 && bytes.Equal(b, []byte("\x7fELF"))
}

// experimentSymbol is the Go symbol a compiler experiment is baked into
// (go build -ldflags "-X mk/internal/toolchain.experiment=...").
const experimentSymbol = "mk/internal/toolchain.experiment"

// refuseELFExperiment is compile-linux's refuseExperiment for the Linux mk,
// which cannot run here, so `mk version` cannot be read: it reads the
// experiment string out of the ELF's data, through its symbol table, before
// the machine lock is taken and WSL started. Go's build information is no
// help, since -trimpath leaves -ldflags out of it. It fails closed, as
// refuseExperiment does: an ELF whose symbol table or experiment variable
// cannot be read is refused. The run inside WSL checks `mk version` again
// (compileTable).
func refuseELFExperiment(path string) error {
	e, err := elfExperiment(path)
	if err != nil {
		return fmt.Errorf("refusing to publish: cannot read %s's %s to check it for a tuning experiment: %v", path, experimentSymbol, err)
	}
	if e != "" {
		return fmt.Errorf("refusing to publish: %s was built with a tuning experiment (experiment: %s); a published table measures a default build only", path, e)
	}
	return nil
}

// elfExperiment is the value of experimentSymbol, a Go string, in the ELF
// at path: "" for a default build, whose variable is in a zero-filled
// section.
func elfExperiment(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		return "", err
	}
	read := func(addr, n uint64) ([]byte, error) {
		for _, s := range f.Sections {
			if s.Flags&elf.SHF_ALLOC == 0 || addr < s.Addr || addr+n > s.Addr+s.Size {
				continue
			}
			if s.Type == elf.SHT_NOBITS {
				return make([]byte, n), nil
			}
			b := make([]byte, n)
			_, err := s.ReadAt(b, int64(addr-s.Addr))
			return b, err
		}
		return nil, fmt.Errorf("no section holds %#x to %#x", addr, addr+n)
	}
	for _, s := range syms {
		if s.Name != experimentSymbol {
			continue
		}
		hdr, err := read(s.Value, 16)
		if err != nil {
			return "", err
		}
		ptr, n := f.ByteOrder.Uint64(hdr), f.ByteOrder.Uint64(hdr[8:])
		if n == 0 {
			return "", nil
		}
		if n > 1<<16 {
			return "", fmt.Errorf("a string header of %d bytes", n)
		}
		b, err := read(ptr, n)
		return string(b), err
	}
	return "", errors.New("no such symbol")
}

// compileLinuxArgs is the wsl.exe command line of the run inside WSL.
func compileLinuxArgs(o compileLinuxOptions, tool, mkVersion string) []string {
	args := []string{"--cd", o.Root, "-e", "taskset", "-c", linuxCompileCPUs, wslPath(tool), "compile",
		"-platform", "linux", "-no-lock", "-publish", "-langs", "mk",
		"-mk", wslPath(o.Mk), "-mk-label", o.Mk + " (" + mkVersion + ")",
		"-modules", strconv.Itoa(o.Spec.Modules), "-funcs", strconv.Itoa(o.Spec.Funcs), "-structs", strconv.Itoa(o.Spec.Structs),
		"-clean-runs", strconv.Itoa(o.CleanRuns), "-edit-runs", strconv.Itoa(o.EditRuns),
		"-warm-runs", strconv.Itoa(o.WarmRuns), "-check-runs", strconv.Itoa(o.CheckRuns),
		"-date", o.Date, "-work", o.LinuxWork}
	return args
}

func compileLinux(o compileLinuxOptions) error {
	if runtime.GOOS != "windows" {
		return errors.New("compile-linux runs on Windows and starts the Linux half inside WSL")
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" {
		return errors.New("compile-linux starts wsl.exe: run it as pwsh -NoProfile -File tools/wsl-lock.ps1 <benchtool> compile-linux ...")
	}
	if strings.HasPrefix(o.LinuxWork, "/mnt/") || !strings.HasPrefix(o.LinuxWork, "/") {
		return fmt.Errorf("-linux-work %q must be an absolute path on the Linux file system, not under /mnt", o.LinuxWork)
	}
	if !isELF(o.Mk) {
		return fmt.Errorf("-mk %s is not a Linux executable: build one with GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o <path> ./cmd/mk at the commit measured", o.Mk)
	}
	if err := refuseELFExperiment(o.Mk); err != nil {
		return err
	}
	if err := o.Spec.check(); err != nil {
		return err
	}
	lock, err := acquireLock(o.LockPath, o.Log)
	if err != nil {
		return err
	}
	defer lock.Release()
	o.Log("holding the machine lock " + lock.Path())
	t, err := findToolchain(o.Work, "")
	if err != nil {
		return err
	}
	tool := filepath.Join(o.Work, "linux", "benchtool")
	gb := exec.Command(t.Go, "build", "-trimpath", "-o", tool, "./tools/bench")
	gb.Dir = o.Root
	gb.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := gb.CombinedOutput(); err != nil {
		return fmt.Errorf("building tools/bench for Linux: %v\n%s", err, firstLines(string(out), 10))
	}
	// The Linux mk cannot run here; Go's build information names its
	// commit, and the run inside WSL records `mk version` and the
	// file's SHA-256 in the facts.
	args := compileLinuxArgs(o, tool, mkIdentity(o.Mk, ""))
	o.Log("wsl.exe " + strings.Join(args, " "))
	cmd := exec.Command("wsl.exe", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the run inside WSL failed: %w", err)
	}
	if _, err := os.Stat(filepath.Join(o.Root, "tests", "bench", "results", o.Date, "results.json")); err == nil {
		return writeResultsMD(o.Root, o.Date)
	}
	o.Log("results/" + o.Date + "/results.json is not there yet, so RESULTS.md is rendered by the main publish of that date")
	return nil
}

// linuxCompileMk is the run inside WSL's copy of the mk it was given, on
// the Linux file system (an executable read over 9P from /mnt would add
// that cost to every build), made executable.
func linuxCompileMk(work, mk string) (string, error) {
	if !strings.HasPrefix(mk, "/mnt/") {
		return mk, nil
	}
	dst := filepath.Join(work, "mk-linux")
	if err := copyVendorFile(mk, dst); err != nil {
		return "", err
	}
	return dst, os.Chmod(dst, 0o755)
}
