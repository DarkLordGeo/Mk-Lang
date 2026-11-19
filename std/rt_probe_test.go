// Why: Linux leaves need to know which file-system operations work under the
// distribution's /tmp and on the Windows drive (/mnt/v, DrvFs).
// What: TestRt_SigbusProbe, opt-in (RT8_PROBE=1 with MK_LINUX=1, under
// tools/wsl-lock.ps1), using the distribution's python3, not an Mk program.
// How: /tmp first, then the drive, where map-and-truncate runs last; a final
// listing of the drive directory shows a stuck bridge as a timeout.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rt8ProbePy probes one directory (argv[1]) and prints "<op> <result>" lines.
const rt8ProbePy = `import errno, fcntl, mmap, os, socket, stat, subprocess, sys
d = sys.argv[1]
drive = sys.argv[2] == "drive"
def say(op, r):
    print(op, r, flush=True)
def err(e):
    return "error:" + errno.errorcode.get(e.errno, str(e.errno)) if isinstance(e, OSError) and e.errno else "error:" + type(e).__name__
def run(op, f):
    try:
        say(op, f())
    except Exception as e:
        say(op, err(e))
def unix():
    p = os.path.join(d, "s.sock")
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        s.bind(p)
        s.listen(1)
        c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        c.settimeout(5)
        c.connect(p)
        c.sendall(b"x")
        a, _ = s.accept()
        a.settimeout(5)
        got = a.recv(1)
        a.close(); c.close()
        return "ok" if got == b"x" else "bad-data"
    finally:
        s.close()
        try: os.unlink(p)
        except OSError: pass
def child(code):
    r = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=20)
    return (r.stdout.strip() or ("rc=" + str(r.returncode)))
def lockprobe(kind):
    p = os.path.join(d, "lock-" + kind)
    open(p, "w").close()
    fd = os.open(p, os.O_RDWR)
    try:
        if kind == "flock":
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            code = "import fcntl,os\nfd=os.open(%r,os.O_RDWR)\ntry:\n fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB); print('acquired')\nexcept OSError as e: print('conflict' if e.errno in (11,) else 'error:'+str(e.errno))" % p
        else:
            fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            code = "import fcntl,os\nfd=os.open(%r,os.O_RDWR)\ntry:\n fcntl.lockf(fd,fcntl.LOCK_EX|fcntl.LOCK_NB); print('acquired')\nexcept OSError as e: print('conflict' if e.errno in (11,13) else 'error:'+str(e.errno))" % p
        other = child(code)
        return "held;other=" + other
    finally:
        os.close(fd)
        os.unlink(p)
def chmod():
    p = os.path.join(d, "perm")
    open(p, "w").close()
    try:
        out = []
        for m in (0o640, 0o755, 0o400):
            os.chmod(p, m)
            out.append("%o->%o" % (m, stat.S_IMODE(os.stat(p).st_mode)))
        os.chmod(p, 0o600)
        return ",".join(out)
    finally:
        os.unlink(p)
def symlink():
    t = os.path.join(d, "target.txt")
    l = os.path.join(d, "link")
    with open(t, "w") as f: f.write("T")
    try:
        os.symlink("target.txt", l)
        kind = "link" if stat.S_ISLNK(os.lstat(l).st_mode) else "notlink"
        with open(l) as f: data = f.read()
        return "%s;readlink=%s;through=%s" % (kind, os.readlink(l), data)
    finally:
        for p in (l, t):
            try: os.unlink(p)
            except OSError: pass
def rename():
    a = os.path.join(d, "a"); b = os.path.join(d, "b")
    with open(a, "w") as f: f.write("old")
    with open(b, "w") as f: f.write("new")
    fd = os.open(a, os.O_RDONLY)
    try:
        os.rename(b, a)
        held = os.read(fd, 16).decode()
        with open(a) as f: now = f.read()
        return "ok;held=%s;path=%s" % (held, now)
    finally:
        os.close(fd)
        for p in (a, b):
            try: os.unlink(p)
            except OSError: pass
def maptrunc():
    p = os.path.join(d, "m")
    with open(p, "wb") as f: f.write(b"A" * 8192)
    if drive:
        fd = os.open(p, os.O_RDONLY)
        try:
            m = mmap.mmap(fd, 8192, mmap.MAP_PRIVATE, mmap.PROT_READ)
            first = chr(m[0])
            try:
                os.truncate(p, 0)
                t = "truncated"
            except OSError as e:
                t = err(e)
            m.close()
            return "mapped=%s;truncate=%s;access-after=not-probed" % (first, t)
        finally:
            os.close(fd)
            os.unlink(p)
    code = "import mmap,os\nfd=os.open(%r,os.O_RDONLY)\nm=mmap.mmap(fd,8192,mmap.MAP_PRIVATE,mmap.PROT_READ)\nprint('first='+chr(m[0]),flush=True)\nos.truncate(%r,0)\nprint('truncated',flush=True)\nx=m[4096]\nprint('read-after='+chr(x),flush=True)" % (p, p)
    r = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=20)
    os.unlink(p)
    return "%s;rc=%d" % (r.stdout.strip().replace("\n", ";"), r.returncode)
say("fs", subprocess.run(["stat", "-f", "-c", "%T", d], capture_output=True, text=True).stdout.strip())
run("unix-socket", unix)
run("flock", lambda: lockprobe("flock"))
run("posix-lock", lambda: lockprobe("lockf"))
run("chmod", chmod)
run("symlink", symlink)
run("rename-over-open", rename)
run("map-truncate", maptrunc)
`

// TestRt_SigbusProbe runs the probe in /tmp, then on the drive, through one
// wsl.exe client, and logs the lines. The probe measures the file systems and
// nothing of std. On the drive the map-and-truncate step runs last and never
// touches the mapping after the truncate: a process killed by SIGBUS while
// holding a 9p file is suspected of hanging the WSL bridge. It asserts only that both halves
// reported every operation; the results themselves are the record.
func TestRt_SigbusProbe(t *testing.T) {
	if os.Getenv("RT8_PROBE") != "1" {
		t.Skip("set RT8_PROBE=1 (with MK_LINUX=1, under the WSL lock) to probe the file systems")
	}
	linuxGate(t)
	dir, err := packageTempDir("mkrt8probe")
	if err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(dir, "drive")
	if err := os.MkdirAll(drive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.py"), []byte(rt8ProbePy), 0o644); err != nil {
		t.Fatal(err)
	}
	py := rtsWSLPath(filepath.Join(dir, "probe.py"))
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\necho \"# kernel $(uname -r)\"\n")
	sh.WriteString("command -v python3 >/dev/null || { echo '# no python3'; exit 0; }\n")
	sh.WriteString(linuxScratchSh("echo '== tmp'; timeout -k 5 60 python3 " + py + " \"$MK_LINUX_TMP\" tmp"))
	sh.WriteString("echo \"# tmp half status $s\"\n")
	sh.WriteString("echo '== drive'; timeout -k 5 60 python3 " + py + " " + rtsWSLPath(drive) + " drive; echo \"# drive half status $?\"\n")
	sh.WriteString("timeout -k 2 10 ls " + rtsWSLPath(drive) + " >/dev/null && echo '# bridge answers' || echo '# bridge did not answer'\n")
	out := linuxRunScript(t, dir, "probe.sh", sh.String(), 240*time.Second)
	t.Logf("\n%s", out)
	for _, half := range []string{"== tmp", "== drive"} {
		i := strings.Index(out, half)
		if i < 0 {
			t.Errorf("no %q half", half)
			continue
		}
		for _, op := range []string{"unix-socket", "flock", "posix-lock", "chmod", "symlink", "rename-over-open", "map-truncate"} {
			if !strings.Contains(out[i:], "\n"+op+" ") {
				t.Errorf("%s: no %s line", half, op)
			}
		}
	}
}
