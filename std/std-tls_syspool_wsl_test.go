// Why: a tls.Client with roots none must read the system's bundle once per
// process, not once per handshake.
// What: TestStdTls_SystemPoolNative, TestStdTls_SystemPoolOnceLinux and the
// SystemPoolOf block tests, each in dev and release.
// How: a driver makes nine handshakes, removing the bundle before the last; on
// Linux strace counts the bundle's opens (MK_LINUX=1, under the WSL lock).

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// tlsSysPoolDriver is the Mk side: argv[1] is the bundle, removed after
// the first handshakes. It prints one line per step.
const tlsSysPoolDriver = `import std.atomic
import std.crypto
import std.encoding
import std.fs
import std.net
import std.tls
import std.x509

const LeafPem: str = "-----BEGIN CERTIFICATE-----\nMIIBfDCCASGgAwIBAgIBAjAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAUMRIwEAYD\nVQQDEwlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARYWAiT0sIB\nPFPoIQColXkYt09CRdmx6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNK\nqIH+y9bVqzPco14wXDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUH\nAwEwHwYDVR0jBBgwFoAUJFRJG8q4njDrib1e2WaLjryUxnkwFAYDVR0RBA0wC4IJ\nbG9jYWxob3N0MAoGCCqGSM49BAMCA0kAMEYCIQD4NROoBKH8KfhkQ/zOMONC60gR\ncRjTYRvpycsxggV3OgIhAKjdsY7ozxVLBHMGzKcJDmUfhRo7ejfd4qjWiC3mdn+n\n-----END CERTIFICATE-----\n"
const KeyPem: str = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgQ3K5SvjaOAx5Gj4e\nrlkGTawIlLQ2SZBirD7hp5nMGbChRANCAARYWAiT0sIBPFPoIQColXkYt09CRdmx\n6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNKqIH+y9bVqzPc\n-----END PRIVATE KEY-----\n"

// Serve answers count connections: one byte back, then the close.
Serve(h: &Heap, io: Io, l: &net.Listener, cfg: &tls.Config, count: int) {
    var buf: [1]u8 = [0]
    for _ in 0..count {
        s := l.Accept(io) else {
            return
        }
        conn := s.Conn()
        c := tls.Server(h, io, conn, cfg) else {
            conn.Abandon()
            continue
        }
        k := c.Read(io, mut buf[..]) else {
            0
        }
        if k == 1 {
            c.WriteAll(io, buf[..]) else {}
        }
        _ = c.Read(io, mut buf[..]) else {
            0
        }
        c.Close(io) else {
            c.Abandon()
        }
    }
}

// Hand is one handshake to addr with cfg and a byte each way: "ok" or the
// error.
Hand(h: &Heap, a: &Arena, io: Io, addr: str, cfg: &tls.Config): OutOfMemory!str from a {
    stream := net.Dial(io, addr) else e {
        return try a.Fmt("err {e}", (e))
    }
    conn := stream.Conn()
    c := tls.Client(h, io, conn, cfg, "localhost") else e {
        conn.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    one: [1]u8 = [7]
    c.WriteAll(io, one[..]) else e {
        c.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    var back: [1]u8 = [0]
    k := c.Read(io, mut back[..]) else e {
        c.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    c.Close(io) else e {
        c.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    return if k == 1 && back[0] == 7 { "ok" } else { "err short" }
}

// Many counts in ok the handshakes of Hand that succeed.
Many(h: &Heap, io: Io, addr: str, cfg: &tls.Config, ok: &atomic.AtomicInt) {
    var a := Arena.New(h) else {
        return
    }
    r := Hand(h, &a, io, addr, cfg) else {
        "err OutOfMemory"
    }
    if r == "ok" {
        _ = ok.Add(1)
    }
    a.Free()
}

export Main(sys: &System): OutOfMemory|WriteError|x509.Error|encoding.Error|crypto.Error|net.Error!void {
    io := sys.io
    a := sys.arena
    bundle := sys.args[1]
    certs := try x509.ParsePem(a, LeafPem)
    blocks := try encoding.ParsePem(a, KeyPem)
    key := try crypto.ParsePrivateKey(blocks[0].der)
    chain := certs.Done()
    ids: [1]tls.Identity = [tls.Identity(chain: chain[..], key: &key)]
    var scfg := tls.DefaultConfig
    scfg.identities = ids[..]
    ccfg := tls.DefaultConfig
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    addr := try a.Fmt("127.0.0.1:{p}", (p: l.Port()))
    ok := atomic.AtomicInt.New(0)
    scope s := io.Scope() {
        s.go Serve(sys.heap, io, &l, &scfg, 9)
        scope m := io.Scope() {
            for _ in 0..8 {
                m.go Many(sys.heap, io, addr, &ccfg, &ok)
            }
        }
        try sys.Print("parallel {n}\n", (n: ok.Load()))
        fs.Remove(io, bundle) else e {
            try sys.Print("remove err {e}\n", (e))
        }
        r := try Hand(sys.heap, a, io, addr, &ccfg)
        try sys.Print("after remove {r}\n", (r))
    }
    fresh := try x509.SystemRoots(a, io)
    try sys.Print("reread {n}\n", (n: fresh.certs.Len()))
    kept := try x509.SystemPool(sys.heap, io)
    try sys.Print("kept {n}\n", (n: kept.certs.Len()))
}
`

// tlsSysPoolRoot is the bench root that signs the driver's leaf.
const tlsSysPoolRoot = "-----BEGIN CERTIFICATE-----\nMIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j\naCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAYMRYwFAYD\nVQQDEw1NayBiZW5jaCByb290MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyTx+\nYlmJmAU6O4dZ0UQ3l5NkWBp3YM0DnNEBOBQ4TupVoFHlQRki7kyY6p+VX9Qrr6Hn\nz8thWyiM/gt642/2dqNCMEAwDgYDVR0PAQH/BAQDAgIEMA8GA1UdEwEB/wQFMAMB\nAf8wHQYDVR0OBBYEFCRUSRvKuJ4w64m9Xtlmi468lMZ5MAoGCCqGSM49BAMCA0gA\nMEUCIQCGfzbV5b0fQ7vvXGWBDlwaBxou4hsIMFapz+zbFYiqbQIgIH6Cu3lYtLDM\n5NakQ34Pul2rlQOLEFZvP0CmTMKU1NE=\n-----END CERTIFICATE-----\n"

// tlsSysPoolWant is the driver's output on Linux.
const tlsSysPoolWant = "parallel 8\nafter remove ok\nreread 0\nkept 1\n"

// TestStdTls_SystemPoolNative: on Windows roots none is the platform's,
// which trusts no test root, so every handshake is refused and the pool
// kept is the platform's (no certificate listed); the driver runs to its
// end, dev and release.
func TestStdTls_SystemPoolNative(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the native case is Windows' platform pool; Linux is TestStdTls_SystemPoolOnceLinux")
	}
	for _, mode := range tlsModes {
		exe := tlsBuildDriver(t, "syspool", mode, tlsSysPoolDriver)
		bundle := filepath.Join(t.TempDir(), "bundle.pem")
		if err := os.WriteFile(bundle, []byte(tlsSysPoolRoot), 0o644); err != nil {
			t.Fatal(err)
		}
		r := runNativeEnv(t, exe, ioCall{args: []string{bundle}, watch: 120})
		got := strings.ReplaceAll(r.stdout, "\r\n", "\n")
		want := "parallel 0\nafter remove err UnknownAuthority\nreread 0\nkept 0\n"
		if r.code != 0 || got != want {
			t.Errorf("%s: exit %d\n got %q\nwant %q\n%s", mode, r.code, got, want, r.stderr)
		}
	}
}

// TestStdTls_SystemPoolOnceLinux: the bundle is opened once per process.
func TestStdTls_SystemPoolOnceLinux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mktlssyspool")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.pem")
	if err := os.WriteFile(root, []byte(tlsSysPoolRoot), 0o644); err != nil {
		t.Fatal(err)
	}
	w := rtsWSLPath
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\nd=$(mktemp -d /tmp/mk-syspool-XXXXXX)\n")
	for _, mode := range tlsModes {
		exe := tlsBuildDriver(t, "syspool-linux", mode, tlsSysPoolDriver, "--target", "x86_64-linux")
		env := `SSL_CERT_FILE="$d/bundle.pem" SSL_CERT_DIR=/nonexistent-mk-syspool `
		fmt.Fprintf(&sh, "echo 'BEGIN %s run'\ncp %s \"$d/bundle.pem\"\n%s%s \"$d/bundle.pem\"\necho \"END $?\"\n", mode, w(root), env, w(exe))
		fmt.Fprintf(&sh, "echo 'BEGIN %s strace'\nif command -v strace >/dev/null 2>&1; then\n", mode)
		fmt.Fprintf(&sh, "  cp %s \"$d/bundle.pem\"\n  %sstrace -f -qq -e trace=open,openat -o \"$d/trace\" %s \"$d/bundle.pem\" >/dev/null\n  s=$?\n", w(root), env, w(exe))
		sh.WriteString("  echo \"opens $(grep 'bundle.pem' \"$d/trace\" | grep -vc ENOENT)\"\n  echo \"END $s\"\nelse\n  echo 'no strace'\n  echo 'END 0'\nfi\n")
	}
	sh.WriteString("rm -rf -- \"$d\"\n")
	out := linuxRunScript(t, dir, "syspool.sh", sh.String(), 5*time.Minute)
	sections := map[string]string{}
	var cur string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "BEGIN "):
			cur = strings.TrimPrefix(line, "BEGIN ")
		case strings.HasPrefix(line, "END "):
			if line != "END 0" {
				t.Errorf("%s: exit %s", cur, strings.TrimPrefix(line, "END "))
			}
			cur = ""
		case cur != "":
			sections[cur] += line + "\n"
		}
	}
	for _, mode := range tlsModes {
		if got := sections[mode+" run"]; got != tlsSysPoolWant {
			t.Errorf("%s: got %q, want %q", mode, got, tlsSysPoolWant)
		}
		switch got := sections[mode+" strace"]; got {
		case "no strace\n":
			t.Logf("%s: no strace in the distribution; the removal shows the one load", mode)
		case "opens 1\n":
			t.Logf("%s: strace: the bundle opened once", mode)
		default:
			t.Errorf("%s strace: got %q, want \"opens 1\"", mode, got)
		}
	}
}

// tlsSysPoolOfBlock is a fixture test block for std/tls (kept out of the
// live tree, whose std.tls blocks other tests count), three blocks:
// SystemPoolOf is SystemPool's pool natively,
// the same kept block at every call and for a NoCancel copy of the native
// Io; under a Sim's Io and its NoCancel copy, a Recorder's over the native
// Io and its NoCancel copy, and the Replayer of that recording, which
// SystemPool refuses, it is the pool SystemRoots reads through that Io (a
// Sim's disk holds none; the Replayer replays the Recorder's reads without
// diverging, which on Linux replays real file reads); and in a cancelled
// scope on the native Io it is Canceled, not SystemRoots' arm.
const tlsSysPoolOfBlock = `import std.raw
import std.sim
import std.x509

// PoUnsupported is whether x509.SystemPool on io fails with Unsupported.
PoUnsupported(h: &Heap, io: Io): bool {
    _ = x509.SystemPool(h, io) else e {
        return match e {
            x509.Unsupported { true }
            _ { false }
        }
    }
    return false
}

test "SystemPoolOf is SystemPool's kept pool natively, the same block each time and for a NoCancel copy" (sys: &System) {
    a := sys.arena
    k1 := x509.SystemPool(sys.heap, sys.io).Must()
    k2 := x509.SystemPool(sys.heap, sys.io).Must()
    Assert(raw.Same(raw.Of(k1), raw.Of(k2)))
    one := SystemPoolOf(sys.heap, a, sys.io).Must()
    two := SystemPoolOf(sys.heap, a, sys.io).Must()
    Assert(one.certs.Len() == k1.certs.Len() && two.certs.Len() == k1.certs.Len())
    if Target.os == Os.Windows {
        Assert(one.certs.Len() == 0)
    }
    k3 := x509.SystemPool(sys.heap, sys.io.NoCancel()).Must()
    Assert(raw.Same(raw.Of(k1), raw.Of(k3)))
}

test "SystemPoolOf under a Sim, a Recorder and its Replayer, and their NoCancel copies, takes SystemRoots' arm" (sys: &System) {
    a := sys.arena
    var s := sim.Sim.New(a, 1).Must()
    Assert(PoUnsupported(sys.heap, s.Io()) && PoUnsupported(sys.heap, s.Io().NoCancel()))
    simulated := SystemPoolOf(sys.heap, a, s.Io()).Must()
    Assert(simulated.certs.Len() == 0)
    s.Free()
    var rec := sim.Recorder.New(sys.io).Must()
    Assert(PoUnsupported(sys.heap, rec.Io()) && PoUnsupported(sys.heap, rec.Io().NoCancel()))
    r := SystemPoolOf(sys.heap, a, rec.Io()).Must()
    if Target.os == Os.Windows {
        Assert(r.certs.Len() == 0)
    }
    log := rec.Log(a).Must()
    rec.Free()
    var rep := sim.Replayer.New(log).Must()
    Assert(PoUnsupported(sys.heap, rep.Io()) && PoUnsupported(sys.heap, rep.Io().NoCancel()))
    q := SystemPoolOf(sys.heap, a, rep.Io()).Must()
    Assert(q.certs.Len() == r.certs.Len())
    Assert(!rep.Diverged())
    rep.Free()
}

test "SystemPoolOf in a cancelled scope on the native Io is Canceled, not SystemRoots' arm" (sys: &System) {
    a := sys.arena
    var canceled := false
    scope sc := sys.io.Scope() {
        sc.Cancel()
        _ = SystemPoolOf(sys.heap, a, sys.io) else e {
            canceled = match e {
                Canceled { true }
                _ { false }
            }
        }
    }
    Assert(canceled)
}
`

// tlsSysPoolOfBlocks is how many blocks tlsSysPoolOfBlock holds.
const tlsSysPoolOfBlocks = 3

// TestStdTls_SystemPoolOfBlock runs tlsSysPoolOfBlock natively, dev and
// release, at 1 and 16 carriers.
func TestStdTls_SystemPoolOfBlock(t *testing.T) {
	tlsSysPoolOfRun(t, nil, []int{1, 16})
}

// TestStdTls_SystemPoolOfBlockLinux runs tlsSysPoolOfBlock on
// x86_64-linux, dev and release, where SystemRoots reads real files, so
// the Recorder records them and the Replayer replays them. MK_LINUX=1 only,
// under the WSL lock with go test -p 1.
func TestStdTls_SystemPoolOfBlockLinux(t *testing.T) {
	linuxGate(t)
	tlsSysPoolOfRun(t, []string{"--target", "x86_64-linux"}, []int{0})
}

// tlsSysPoolOfRun runs tlsSysPoolOfBlock with target's flags, dev and
// release, once per carrier count (0: the default).
func tlsSysPoolOfRun(t *testing.T, target []string, carriers []int) {
	for _, c := range carriers {
		for _, flags := range [][]string{nil, {"--release"}} {
			t.Run(fmt.Sprintf("carriers=%d%s", c, strings.Join(flags, "")), func(t *testing.T) {
				root := t.TempDir()
				stdDir := tlsStdDir(t, root, map[string]string{"tls/tests_syspoolof.mk": tlsSysPoolOfBlock})
				t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
				if c > 0 {
					t.Setenv("MK_CARRIERS", fmt.Sprint(c))
				}
				args := append([]string{"test", "--std=std.tls", "-run", "SystemPoolOf"}, target...)
				out, status := mkCmd(t, root, stdDir, append(args, flags...)...)
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				if status != 0 || strings.Count(out, "PASS std.tls") != tlsSysPoolOfBlocks {
					t.Fatalf("status %d, want %d passes\n%s", status, tlsSysPoolOfBlocks, out)
				}
			})
		}
	}
}
