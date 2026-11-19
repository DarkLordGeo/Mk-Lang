// Why: tests and mk run start a target's executable, which on Windows may
// be a Linux binary that only runs through WSL.
//
// What: Runner, the argv prefix that runs a triple's executable here, and
// HostPathFor.
//
// How: the host's own OS and architecture need no prefix; x86_64 Linux on
// an x86_64 Windows host runs through wsl.exe -e, with drive paths
// rewritten to /mnt/<drive>/ by HostPathFor.

package toolchain

import (
	"os/exec"
	"runtime"
	"strings"
)

// Runner returns the argv prefix that runs an executable of the triple on
// this host: nil for the host's own OS and architecture, [<wsl.exe>, "-e"]
// (wsl.exe by its absolute path) for x86_64 Linux on an x86_64 Windows host
// with wsl.exe present, and ok=false when no runner exists. Paths given to a
// WSL runner are rewritten by HostPathFor.
func Runner(triple string) (prefix []string, ok bool) {
	return runnerFor(triple, runtime.GOOS, runtime.GOARCH, exec.LookPath)
}

func runnerFor(triple, goos, goarch string, lookPath func(string) (string, error)) ([]string, bool) {
	tos, tarch := OSOf(triple), ArchOf(triple)
	if tos == "" || tarch != goarch {
		return nil, false
	}
	if tos == goos {
		return nil, true
	}
	if tos == "linux" && goos == "windows" && goarch == "amd64" {
		// exec.LookPath never returns a path relative to the current
		// directory (Go 1.19 ErrDot), so p is absolute or a PATH entry's.
		if p, err := lookPath("wsl.exe"); err == nil {
			return []string{p, "-e"}, true
		}
	}
	return nil, false
}

// HostPathFor rewrites a host path for the runner prefix: for a WSL prefix,
// a drive path `C:\a\b` becomes `/mnt/c/a/b`; any other path, and every
// path for a nil prefix, is returned unchanged.
func HostPathFor(prefix []string, path string) string {
	if len(prefix) == 0 || !strings.EqualFold(baseName(prefix[0]), "wsl.exe") {
		return path
	}
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return path
	}
	drive := path[0] | 0x20
	if drive < 'a' || drive > 'z' {
		return path
	}
	return "/mnt/" + string(drive) + "/" + strings.ReplaceAll(path[3:], `\`, "/")
}

// baseName is the last element of a Windows or slash path on any host.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
