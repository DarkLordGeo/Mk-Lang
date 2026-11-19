// Why: interactive dogfoods (the fm file manager) need cases that drive
// a real terminal, and programs that use Unix sockets or permission bits
// need a scratch directory on the target's own file system.
//
// What: ptyrunExe (ptyrun built with the dogfood's mk), ptyArgv,
// ptyLimitMs and ptyResult for terminal cases; hostTmp, linuxTmp,
// tmpScript, wrapTmp and sweepArgv for {tmp} scratch directories.
//
// How: a .pty case runs its program under ptyrun (tools/dogfood/ptyrun, Mk
// on proc.Pty and proc.Spawn), which types the script into a
// pseudo-terminal and prints one status line; the case is judged on the
// exit code and on $WORK/transcript, never on the terminal's bytes. {tmp}
// is a fresh directory beside the working one on Windows, and under the
// distribution's /tmp on Linux (the exec-jobs child makes it, sets
// MK_LINUX_TMP and removes it).

package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ptyMargin is how much sooner than the case's time ptyrun's own limit
// ends, so ptyrun kills the program and reports before the harness kills
// ptyrun.
const ptyMargin = 3 * time.Second

// transcriptName is the file in the case's working directory a terminal
// case's program writes its transcript to ($WORK/transcript).
const transcriptName = "transcript"

// ptyrunDir is ptyrun's package.
func (h *harness) ptyrunDir() string {
	return filepath.Join(h.root, "tools", "dogfood", "ptyrun")
}

// ptyrunExe builds ptyrun for target once per run (dev profile, never
// with --pgo-gen: its own calls are no dogfood's coverage) and returns
// the executable.
func (h *harness) ptyrunExe(target string) (string, error) {
	if h.ptyrun == nil {
		h.ptyrun = map[string]string{}
	}
	if v, ok := h.ptyrun[target]; ok {
		if strings.HasPrefix(v, "!") {
			return "", fmt.Errorf("%s", v[1:])
		}
		return v, nil
	}
	exe := filepath.Join(h.scratch, "bin", "ptyrun", target, "ptyrun")
	if target == targetWindows {
		exe += ".exe"
	}
	err := os.MkdirAll(filepath.Dir(exe), 0o755)
	if err == nil {
		cmd := exec.Command(h.mk, "build", "--target", target, "-o", exe, h.ptyrunDir())
		cmd.Dir = h.ptyrunDir()
		cmd.Env = append(h.childEnv(), "MK_CACHE="+filepath.Join(h.work, "dogfood", "cache", "ptyrun"))
		var out []byte
		if out, err = cmd.CombinedOutput(); err != nil {
			err = fmt.Errorf("building ptyrun for %s: %v: %s", target, err, strings.Join(tail(lines(out), 20), " | "))
		}
	}
	if err != nil {
		h.ptyrun[target] = "!" + err.Error()
		return "", err
	}
	h.say("  built ptyrun for %s", target)
	h.ptyrun[target] = exe
	return exe, nil
}

// ptyArgv is the command line of a terminal case: ptyrun, its limit, the
// file it keeps the terminal's bytes in, the transcript and the script,
// then the program and its arguments. Paths are as the process sees them.
func ptyArgv(ptyrun string, limit time.Duration, raw, transcript, script string, program []string) []string {
	argv := []string{ptyrun, "--limit", ptyLimitMs(limit), "--raw", raw, "--transcript", transcript, "--script", script, "--"}
	return append(argv, program...)
}

// ptyLimitAt is the index of the --limit value in ptyArgv's command line.
const ptyLimitAt = 2

// ptyLimitMs is ptyrun's --limit for a case with limit left: the margin
// less, at least a second.
func ptyLimitMs(limit time.Duration) string {
	return strconv.FormatInt(max(1000, (limit-ptyMargin).Milliseconds()), 10)
}

// ptyRaw is where a run of a terminal case keeps the terminal's bytes:
// beside its working directory, so the program never sees the file.
func ptyRaw(work string) string {
	return work + ".pty-raw"
}

// ptyResult turns ptyrun's run into the case's result: its status line
// gives the program's exit code (128 plus the number for a signal, as a
// shell reports it), the transcript in work is the output judged as
// stdout, and ptyrun's own stderr stays stderr, empty when it works.
func ptyResult(r result, work string) result {
	if r.err != "" || r.timedOut {
		return r
	}
	out := result{stderr: r.stderr, seconds: r.seconds}
	line := strings.TrimSpace(string(r.stdout))
	word, rest, _ := strings.Cut(line, " ")
	switch {
	case word == "exit" && r.code == 0:
		n, err := strconv.Atoi(rest)
		if err != nil {
			out.err = fmt.Sprintf("ptyrun printed %q", line)
		}
		out.code = n
	case word == "signal" && r.code == 0:
		n, ok := signalNumbers[rest]
		if !ok {
			out.err = fmt.Sprintf("ptyrun printed %q, a signal the harness does not know", line)
		}
		out.code = 128 + n
	case word == "error":
		out.err = "ptyrun: " + rest
	default:
		out.err = fmt.Sprintf("ptyrun printed %q and exited %d, not one line `exit N`, `signal NAME` or `error WHY`", line, r.code)
	}
	b, err := os.ReadFile(filepath.Join(work, transcriptName))
	switch {
	case err == nil:
		out.stdout = b
	case !os.IsNotExist(err) && out.err == "":
		out.err = "reading the transcript: " + err.Error()
	}
	return out
}

// signalNumbers are proc.Signal's members by the name ptyrun prints, with
// their Linux numbers.
var signalNumbers = map[string]int{"Hangup": 1, "Interrupt": 2, "Terminate": 15}

// hostTmp makes the empty scratch directory of one run of a case on the
// host: <work>.tmp, beside the working directory (the case's {tmp}).
func hostTmp(work string) (string, error) {
	d := work + ".tmp"
	if err := os.RemoveAll(d); err != nil {
		return "", err
	}
	return d, os.MkdirAll(d, 0o755)
}

// linuxTmpRoot is the directory under the distribution's /tmp that holds
// this run's scratch directories: /tmp/mk-<run>, <run> the base name of
// the run's own directory, so harnesses running at once never share one.
func (h *harness) linuxTmpRoot() string {
	return "/tmp/mk-" + filepath.Base(h.scratch)
}

// linuxTmp is one Linux run's scratch directory:
// /tmp/mk-<run>/<dogfood>.<profile>.<case>[.<seed>].
func (h *harness) linuxTmp(d *Dogfood, rr *RunReport, c *Case, seed string) string {
	n := d.Name + "." + rr.Profile + "." + c.Name
	if rr.profiles != "" {
		n += ".pgo"
	}
	if seed != "" {
		n += "." + seed
	}
	return h.linuxTmpRoot() + "/" + n
}

// tmpScript is the shell the exec-jobs child puts between `wsl.exe -e`
// and the program of a job with a scratch directory: it makes the
// directory (and /tmp/mk-<run>), runs the program with MK_LINUX_TMP set,
// removes the directory, and exits with the program's status. It is
// outside `timeout`'s process group, so it cleans up after a timeout too.
const tmpScript = `d=$1; shift
case $d in /tmp/mk-run-*/?*) ;; *) echo "dogfood: refusing scratch path $d" >&2; exit 125;; esac
mkdir -p -m 700 -- "$d" || exit 125
MK_LINUX_TMP=$d "$@"
c=$?
rm -rf -- "$d"
exit $c`

// wrapTmp puts tmpScript, with tmp as its first argument, right after the
// -e of a `wsl.exe ... -e <prog> <args>` command line; argv is unchanged
// when it has no -e.
func wrapTmp(argv []string, tmp string) []string {
	for i, a := range argv {
		if a != "-e" || i+1 >= len(argv) {
			continue
		}
		out := append([]string{}, argv[:i+1]...)
		out = append(out, "sh", "-c", tmpScript, "sh", tmp)
		return append(out, argv[i+1:]...)
	}
	return argv
}

// sweepArgv removes what is left of a run's scratch root on Linux after
// its jobs, such as a directory whose job's wsl.exe was killed from
// Windows before its shell could remove it.
func sweepArgv(wsl, root string) []string {
	return []string{wsl, "-e", "sh", "-c", `case $1 in /tmp/mk-run-*) rm -rf -- "$1";; esac`, "sh", root}
}

// hasPtyCase reports whether a dogfood has a terminal case: a .pty file
// directly in its cases/.
func hasPtyCase(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "cases", "*.pty"))
	return len(m) > 0
}

// ptyrunImports are the std modules ptyrun's sources import.
func ptyrunImports(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".mk") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range importLine.FindAllSubmatch(b, -1) {
			if n := string(m[1]); !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}
