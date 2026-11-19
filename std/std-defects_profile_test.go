// Why: a child killed while it exits must not leave a truncated profile, which
// mk profile merge refuses.
// What: TestKilledWhileWritingLeavesNoBrokenProfile.
// How: kills a --pgo-gen program at growing delays after its last output line,
// so the kills sweep the moment Finish writes the profile.

package std

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// profKillSrc is a --pgo-gen program whose profile table is large (each
// of its functions adds a record with one counter per branch), so writing
// it takes a while. It prints "bye" and returns; Finish flushes stdout
// before it writes the profile, so the line arrives just before the write.
func profKillSrc(funcs int) string {
	var b strings.Builder
	for i := 0; i < funcs; i++ {
		fmt.Fprintf(&b, "F%d(x: int): int {\n    var s := x\n", i)
		for j := 0; j < 8; j++ {
			fmt.Fprintf(&b, "    if s %% %d == %d {\n        s += %d\n    }\n", j+2, j%2, i+j)
		}
		b.WriteString("    return s\n}\n\n")
	}
	b.WriteString("export Main(sys: &System): WriteError!void {\n    var t := 0\n")
	for i := 0; i < funcs; i++ {
		fmt.Fprintf(&b, "    t = (t + F%d(t)) %% 1000003\n", i)
	}
	b.WriteString("    try sys.Print(\"bye {t}\\n\", (t))\n}\n")
	return b.String()
}

// A process killed while it writes its profile leaves no
// broken profile under the name mk profile merge reads. Each run is
// killed a growing delay after its last output line, which Finish writes
// just before the profile, so the kills sweep the write. Every
// <name>-<pid>.mkprof left must be non-empty and merge; a kill that
// landed mid-write leaves only the temporary <name>-<pid>.mkprof.tmp.
// With the writer that truncated the final name first, those kills left
// a 0-byte or cut .mkprof. When no kill of the sweep lands mid-write (a
// loaded machine), nothing was tested and the test skips.
func TestKilledWhileWritingLeavesNoBrokenProfile(t *testing.T) {
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(profKillSrc(3000)), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "kp.exe")
	if out, st := mkCmd(t, root, stdDir, "build", "--release", "--pgo-gen", "-o", exe, "main.mk"); st != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build --pgo-gen: exit %d\n%s", st, out)
	}
	dir := t.TempDir()
	const runs = 40
	midWrite, whole := 0, 0
	for i := 0; i < runs; i++ {
		pid, err := profKillRun(exe, dir, time.Duration(i)*50*time.Microsecond)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		base := filepath.Join(dir, fmt.Sprintf("kp-%d.mkprof", pid))
		if _, err := os.Stat(base + ".tmp"); err == nil {
			midWrite++
		}
		fi, err := os.Stat(base)
		if err != nil {
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("run %d (delay %d us): %s is empty", i, i*50, filepath.Base(base))
			continue
		}
		merged := filepath.Join(t.TempDir(), "m.mkprof")
		if out, st := mkCmd(t, root, stdDir, "profile", "merge", "-o", merged, base); st != 0 {
			t.Errorf("run %d (delay %d us): mk profile merge refuses %s (%d bytes): exit %d\n%s",
				i, i*50, filepath.Base(base), fi.Size(), st, out)
			continue
		}
		whole++
	}
	t.Logf("%d runs: %d killed while writing (a .tmp left), %d left a whole profile", runs, midWrite, whole)
	if midWrite == 0 && !t.Failed() {
		t.Skipf("no kill of the sweep landed while the profile was being written; nothing was tested")
	}
}

// profKillRun runs exe with MK_PROFILE_DIR=dir, kills it delay after its
// output line arrives, and returns its pid once it has exited.
func profKillRun(exe, dir string, delay time.Duration) (int, error) {
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "MK_PROFILE_DIR="+dir, "MK_CARRIERS=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	done := make(chan struct{})
	timer := time.AfterFunc(60*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		if strings.HasPrefix(line, "bye") {
			start := time.Now()
			for time.Since(start) < delay {
			}
		}
		_ = cmd.Process.Kill()
		close(done)
	}()
	<-done
	_ = cmd.Wait()
	return pid, nil
}
