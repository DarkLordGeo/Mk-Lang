// Why: Linux leaves keep their working directory on the Windows drive (/mnt/v),
// where Unix sockets, locks, links and permission bits do not behave.
// What: linuxScratchSh, which gives a case $MK_LINUX_TMP under the
// distribution's /tmp, and TestRt_LinuxScratchShape, TestRt_LinuxScratch.
// How: the helper only writes shell text, so a leaf still runs its whole script
// through one wsl.exe client under linuxGate, wslGuard and the WSL lock.

package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// linuxScratchSh returns POSIX sh text that runs cmd in a subshell with
// MK_LINUX_TMP set to a new directory under /tmp (mode 0700, from mktemp),
// removes the directory afterwards, and leaves cmd's exit status in $s. A
// failed mktemp leaves s=125 and runs nothing. cmd is shell text, run as
// written; it may name "$MK_LINUX_TMP".
func linuxScratchSh(cmd string) string {
	var b strings.Builder
	b.WriteString("d=$(mktemp -d /tmp/mk-std-XXXXXX)\n")
	b.WriteString("if [ -z \"$d\" ]; then s=125; else\n")
	b.WriteString("  ( export MK_LINUX_TMP=\"$d\"; " + cmd + " )\n")
	b.WriteString("  s=$?\n")
	b.WriteString("  rm -rf -- \"$d\"\n")
	b.WriteString("fi\n")
	return b.String()
}

// linuxRunScript writes script into dir as name and runs it with sh through
// one wsl.exe client, bounded inside the VM by `timeout -k 5 <limit>`, and
// returns its standard output. The caller has called linuxGate; this checks
// WSL answers (rtsNeedWSL) and the lock (wslGuard) before it starts.
func linuxRunScript(t *testing.T, dir, name, script string, limit time.Duration) string {
	t.Helper()
	rtsNeedWSL(t)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	secs := int(limit / time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), limit+30*time.Second)
	defer cancel()
	wslGuard()
	cmd := exec.CommandContext(ctx, "wsl.exe", "-e", "timeout", "-k", "5", strconv.Itoa(secs), "sh", rtsWSLPath(p))
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wsl.exe sh %s: %v\n%s", name, err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

// TestRt_LinuxScratchShape: the helper's text, on Windows (no wsl.exe).
func TestRt_LinuxScratchShape(t *testing.T) {
	sh := linuxScratchSh("echo hi > \"$MK_LINUX_TMP/f\"")
	for _, want := range []string{"mktemp -d /tmp/mk-std-", "export MK_LINUX_TMP=\"$d\"", "s=$?", "rm -rf -- \"$d\""} {
		if !strings.Contains(sh, want) {
			t.Errorf("linuxScratchSh lacks %q:\n%s", want, sh)
		}
	}
	if strings.Index(sh, "s=$?") > strings.Index(sh, "rm -rf") {
		t.Error("the status must be taken before the directory is removed")
	}
}

// TestRt_LinuxScratch runs two cases through the helper in one wsl.exe
// client: one that succeeds and one that fails, each writing a file in its
// directory. Each sees its own fresh directory under /tmp, the status comes
// through, and both directories are gone afterwards. MK_LINUX=1, under the
// WSL lock.
func TestRt_LinuxScratch(t *testing.T) {
	linuxGate(t)
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	for i, c := range []string{"exit 0", "exit 3"} {
		sh.WriteString(linuxScratchSh("echo \"case" + strconv.Itoa(i+1) + " $MK_LINUX_TMP\"; touch \"$MK_LINUX_TMP/x\" && " + c))
		sh.WriteString("echo \"status" + strconv.Itoa(i+1) + " $s\"\n")
		sh.WriteString("if [ -e \"$d\" ]; then echo \"left" + strconv.Itoa(i+1) + "\"; else echo \"gone" + strconv.Itoa(i+1) + "\"; fi\n")
	}
	dir, err := packageTempDir("mkrt8tmp")
	if err != nil {
		t.Fatal(err)
	}
	out := linuxRunScript(t, dir, "scratch.sh", sh.String(), 60*time.Second)
	var dirs []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "case") {
			f := strings.Fields(l)
			if len(f) != 2 || !strings.HasPrefix(f[1], "/tmp/mk-std-") {
				t.Errorf("case line %q: want a directory under /tmp", l)
				continue
			}
			dirs = append(dirs, f[1])
		}
	}
	if len(dirs) != 2 || dirs[0] == dirs[1] {
		t.Errorf("directories %v: want two distinct ones\n%s", dirs, out)
	}
	for _, want := range []string{"status1 0", "status2 3", "gone1", "gone2"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
