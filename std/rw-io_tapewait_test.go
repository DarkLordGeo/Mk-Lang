// Why: a wait under a Recorder's Io must sleep, not spin every carrier.
// What: TestRwIo_RecorderWaitSleeps, for std/sim/tape.mk's TWait.
// How: eight tasks under Recorder.New(sys.io).Io() wait 2 s on a channel that
// main fills after a native sleep. A wait that yields and returns spins
// every carrier for the whole 2 s; a wait on the inner Io costs nearly no
// CPU. The verdict is the process's own CPU time (user plus system, read
// by Go from the OS when the process has exited), so the machine's load
// moves it little; it is still a measurement, so it runs only with
// MK_TIMING=1, as every timing assertion does. Windows host, dev build.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const rwIoTapeWaitMain = `import std.sim

Wait(io: Io, ch: &Chan[int], got: &Chan[int]) {
    v := ch.Recv(io) else {
        -1
    }
    got.Send(io, v) else {
    }
}

export Main(sys: &System): OutOfMemory!void {
    a := sys.heap.Alloc()
    var rec := try sim.Recorder.New(sys.io)
    io := rec.Io()
    ch := Chan[int].New(a, 8) else {
        rec.Free()
        fail OutOfMemory
    }
    got := Chan[int].New(a, 8) else {
        ch.Free(a)
        rec.Free()
        fail OutOfMemory
    }
    var sum := 0
    scope s := io.Scope() {
        for _ in 0..8 {
            s.go Wait(io, &ch, &got)
        }
        sys.io.Sleep(time.Millis(2000)) else {
        }
        for i in 0..8 {
            ch.Send(io, i + 1) else {
            }
        }
        for _ in 0..8 {
            sum += got.Recv(io) else {
                0
            }
        }
    }
    got.Free(a)
    ch.Free(a)
    rec.Free()
    var out := sys.Stdout()
    _ = Print(mut out, "sum {sum}\n", (sum)) else {
    }
    _ = out.Flush() else {
    }
}
`

func TestRwIo_RecorderWaitSleeps(t *testing.T) {
	if os.Getenv("MK_TIMING") != "1" {
		t.Skip("a CPU-time measurement: runs with MK_TIMING=1 (tools/gate's timing tier)")
	}
	root := t.TempDir()
	stdDir := htStd(t, root, nil)
	dir := filepath.Join(root, "prog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(rwIoTapeWaitMain, "import std.sim\n", "import std.sim\nimport std.time\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	exe := filepath.Join(dir, "wait")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, status := mkCmd(t, dir, stdDir, "build", "-o", exe, "main.mk")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	for _, carriers := range []string{"1", "8"} {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "MK_CARRIERS="+carriers)
		t0 := time.Now()
		b, err := cmd.Output()
		wall := time.Since(t0)
		if err != nil {
			t.Fatalf("MK_CARRIERS=%s: %v\n%s", carriers, err, b)
		}
		if got := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n")); got != "sum 36" {
			t.Fatalf("MK_CARRIERS=%s: printed %q, want sum 36", carriers, got)
		}
		cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
		t.Logf("MK_CARRIERS=%s: wall %v, CPU %v", carriers, wall, cpu)
		if cpu > 500*time.Millisecond {
			t.Errorf("MK_CARRIERS=%s: %v of CPU over a 2 s wait (wall %v): the waiters spin", carriers, cpu, wall)
		}
	}
}
