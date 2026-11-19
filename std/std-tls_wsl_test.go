// Why: std.tls must behave the same on x86_64-linux as on Windows.
// What: TestStdTls_Linux: the RFC 8448 records, every pair case and both Go
// peer directions, in dev and release.
// How: one wsl.exe client runs everything; the Go peer is cross-compiled for
// Linux so both ends share the VM's loopback (MK_LINUX=1, under
// tools/wsl-lock.ps1).

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlsLinuxHelper is the Go side on Linux. "serve chain key curves protos
// driver args..." runs a crypto/tls echo server for one connection and the
// driver with ADDR in its arguments replaced by the server's address;
// "dial root curves protos max payload driver args..." starts the Mk
// server driver, reads its port and runs a crypto/tls client that echoes
// payload bytes and closes. Each prints "D <the driver's line>" and
// "G <what Go saw>".
const tlsLinuxHelper = `package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func curves(s string) []tls.CurveID {
	var out []tls.CurveID
	if s == "-" {
		return nil
	}
	for _, p := range strings.Split(s, ",") {
		n, _ := strconv.Atoi(p)
		out = append(out, tls.CurveID(n))
	}
	return out
}

func list(s string) []string {
	if s == "-" {
		return nil
	}
	return strings.Split(s, ",")
}

func serve(a []string) {
	cert, err := tls.LoadX509KeyPair(a[0], a[1])
	if err != nil {
		fmt.Println("G helper", err)
		return
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13,
		CurvePreferences: curves(a[2]), NextProtos: list(a[3])}
	l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		fmt.Println("G helper", err)
		return
	}
	done := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- "err " + err.Error()
			return
		}
		defer c.Close()
		tc := c.(*tls.Conn)
		tc.SetDeadline(time.Now().Add(60 * time.Second))
		if err := tc.Handshake(); err != nil {
			done <- "err " + err.Error()
			return
		}
		buf := make([]byte, 65536)
		var rerr error
		for {
			n, err := tc.Read(buf)
			if n > 0 {
				if _, werr := tc.Write(buf[:n]); werr != nil {
					rerr = werr
					break
				}
			}
			if err != nil {
				rerr = err
				break
			}
		}
		st := tc.ConnectionState()
		done <- fmt.Sprintf("ok version=%x suite=%x curve=%d eof=%v", st.Version, st.CipherSuite, st.CurveID, rerr == io.EOF)
	}()
	args := append([]string(nil), a[5:]...)
	for i := range args {
		if args[i] == "ADDR" {
			args[i] = l.Addr().String()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, a[4], args...).Output()
	fmt.Printf("D %s\n", strings.TrimSpace(string(out)))
	select {
	case g := <-done:
		fmt.Println("G " + g)
	case <-time.After(3 * time.Second):
		l.Close()
		fmt.Println("G " + <-done)
	}
	l.Close()
}

func dial(a []string) {
	pem, err := os.ReadFile(a[0])
	if err != nil {
		fmt.Println("G helper", err)
		return
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	max, _ := strconv.Atoi(a[3])
	n, _ := strconv.Atoi(a[4])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a[5], a[6:]...)
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		fmt.Println("G helper", err)
		return
	}
	r := bufio.NewReader(stdout)
	first, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, "port ") {
		cmd.Wait()
		fmt.Printf("D %s\nG no port\n", strings.TrimSpace(first))
		return
	}
	addr := "127.0.0.1:" + strings.TrimSpace(strings.TrimPrefix(first, "port "))
	cfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13,
		CurvePreferences: curves(a[1]), NextProtos: list(a[2])}
	if max != 0 {
		cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS12, uint16(max)
	}
	g := func() string {
		conn, err := tls.Dial("tcp", addr, cfg)
		if err != nil {
			return "err " + err.Error()
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(60 * time.Second))
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*3 + 1)
		}
		werr := make(chan error, 1)
		go func() {
			_, err := conn.Write(data)
			werr <- err
		}()
		back := make([]byte, n)
		if _, err := io.ReadFull(conn, back); err != nil {
			return "err echo " + err.Error()
		}
		if err := <-werr; err != nil {
			return "err write " + err.Error()
		}
		for i := range back {
			if back[i] != data[i] {
				return fmt.Sprintf("err byte %d differs", i)
			}
		}
		conn.CloseWrite()
		_, rerr := conn.Read(make([]byte, 1))
		st := conn.ConnectionState()
		alpn := st.NegotiatedProtocol
		if alpn == "" {
			alpn = "-"
		}
		return fmt.Sprintf("ok version=%x suite=%x curve=%d alpn=%s eof=%v", st.Version, st.CipherSuite, st.CurveID, alpn, rerr == io.EOF)
	}()
	rest, _ := io.ReadAll(r)
	cmd.Wait()
	fmt.Printf("D %s\nG %s\n", strings.TrimSpace(string(rest)), g)
}

func main() {
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "dial":
		dial(os.Args[2:])
	}
}
`

// tlsLinuxPeer is one tls.Client case against Go's server on Linux.
type tlsLinuxPeer struct {
	name, leaf, roots, suites, curves, protos, alpn, mode string
	n                                                     int
	want, goWant                                          string // the driver's line and Go's, each a prefix when it ends in "*"
}

// tlsLinuxPeers are the client cases: each suite, both groups (P-256
// through a HelloRetryRequest), each leaf key type, ALPN, a refused chain,
// 1 MiB, the connector, and Close past its limit.
var tlsLinuxPeers = []tlsLinuxPeer{
	{name: "aes128gcm", leaf: "p256", suites: "4865", want: "ok alpn=- n=5", goWant: "ok version=304 suite=1301 curve=29 eof=true"},
	{name: "aes256gcm", leaf: "p256", suites: "4866", want: "ok alpn=- n=5", goWant: "ok version=304 suite=1302 curve=29 eof=true"},
	{name: "chacha20poly1305", leaf: "p256", suites: "4867", want: "ok alpn=- n=5", goWant: "ok version=304 suite=1303 curve=29 eof=true"},
	{name: "p256 through HelloRetryRequest", leaf: "p256", curves: "23", want: "ok alpn=- n=5", goWant: "ok version=304 suite=1301 curve=23 eof=true"},
	{name: "leaf p384", leaf: "p384", want: "ok alpn=- n=5", goWant: "ok version=304*"},
	{name: "leaf ed25519", leaf: "ed25519", want: "ok alpn=- n=5", goWant: "ok version=304*"},
	{name: "leaf rsa-pss", leaf: "rsa", want: "ok alpn=- n=5", goWant: "ok version=304*"},
	{name: "alpn server preference", leaf: "p256", alpn: "http/1.1,h2", protos: "h2,http/1.1", want: "ok alpn=h2 n=5", goWant: "ok version=304*"},
	{name: "unknown authority", leaf: "p256", roots: "other", want: "err UnknownAuthority", goWant: "err *unknown certificate authority*"},
	{name: "one MiB both ways", leaf: "p256", n: 1 << 20, want: "ok alpn=- n=1048576", goWant: "ok version=304 suite=1301 curve=29 eof=true"},
	{name: "connector", leaf: "p256", mode: "connector", want: "ok alpn=- n=5", goWant: "ok version=304*"},
	{name: "close past its limit", leaf: "p256", mode: "stall", want: "closeerr TimedOut ms=*", goWant: "*"},
	// roots: none, DefaultConfig's own path: x509.SystemRoots reads
	// SSL_CERT_FILE (and SSL_CERT_DIR, here a directory that does not
	// exist), so the test root is the system's for this one run.
	{name: "system roots through SSL_CERT_FILE", leaf: "p256", roots: "system", want: "ok alpn=- n=5", goWant: "ok version=304*"},
	{name: "system roots without the root", leaf: "p256", roots: "system-other", want: "err UnknownAuthority", goWant: "err *unknown certificate authority*"},
}

// tlsLinuxServer is one tls.Server case against Go's client on Linux.
type tlsLinuxServer struct {
	name, leaf, curves, protos, client string
	max, n                             int
	want, goWant                       string
	env                                string // before the helper's command, as VAR=value
}

var tlsLinuxServers = []tlsLinuxServer{
	{name: "p256 key x25519", leaf: "p256", want: "served alpn=- n=5", goWant: "ok version=304 suite=1301 curve=29 alpn=- eof=true"},
	{name: "ed25519 key", leaf: "ed25519", want: "served alpn=- n=5", goWant: "ok version=304*"},
	{name: "HelloRetryRequest for p256", leaf: "p256", curves: "24,23", want: "served alpn=- n=5", goWant: "ok version=304 suite=1301 curve=23 alpn=- eof=true"},
	{name: "alpn server order", leaf: "p256", protos: "h2,http/1.1", client: "http/1.1,h2", want: "served alpn=h2 n=5", goWant: "ok version=304 suite=1301 curve=29 alpn=h2 eof=true"},
	{name: "tls 1.2 client", leaf: "p256", max: 0x0303, want: "err Unsupported", goWant: "err *protocol version*"},
	{name: "one MiB", leaf: "p256", n: 1 << 20, want: "served alpn=- n=1048576", goWant: "ok version=304*"},
	// Go's client without AES (GODEBUG, read at start on Linux)
	// offers ChaCha20-Poly1305 first, so the server role meets Go's
	// crypto/tls over it.
	{name: "chacha20poly1305 Go without AES", leaf: "p256", n: 1 << 20, env: "GODEBUG=cpu.aes=off ", want: "served alpn=- n=1048576", goWant: "ok version=304 suite=1303 curve=29 alpn=- eof=true"},
}

// tlsGlob matches s against a pattern whose "*"s match any text.
func tlsGlob(s, pat string) bool {
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i, p := range parts[1:] {
		if i == len(parts)-2 {
			return strings.HasSuffix(s, p)
		}
		k := strings.Index(s, p)
		if k < 0 {
			return false
		}
		s = s[k+len(p):]
	}
	return true
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// TestStdTls_Linux runs the oracle, the pair cases and both roles against
// Go on x86_64-linux, dev and release, in one script.
func TestStdTls_Linux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mktlslinux")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tlsgo.go")
	if err := os.WriteFile(src, []byte(tlsLinuxHelper), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "tlsgo")
	cmd := exec.Command("go", "build", "-o", helper, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the Linux helper: %v\n%s", err, o)
	}
	pki := tlsPeerPKI(t)
	leaves := map[string][3]string{}
	for _, l := range []string{"p256", "p384", "ed25519", "rsa"} {
		c, k, r := tlsLeafFiles(t, dir, pki, l)
		leaves[l] = [3]string{c, k, r}
	}
	other := filepath.Join(dir, "other.pem")
	if err := os.WriteFile(other, []byte(pki.otherPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := map[string][]tlsCase{
		"keyschedule": tlsKeyScheduleCases(t),
		"records":     tlsRecordCases(t),
		"messages":    tlsMessageCases(t),
	}
	for name, cases := range oracle {
		var in strings.Builder
		for _, c := range cases {
			in.WriteString(c.record + "\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name+".in"), []byte(in.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := rtsWSLPath
	linux := []string{"--target", "x86_64-linux"}
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	for _, mode := range tlsModes {
		drv := tlsBuildDriver(t, "driver-linux", mode, tlsDriver, linux...)
		pair := tlsBuildDriver(t, "pair-linux", mode, tlsPairDriver, linux...)
		peer := tlsBuildDriver(t, "peer-linux", mode, tlsPeerDriver, linux...)
		srv := tlsBuildDriver(t, "server-linux", mode, tlsServerDriver, linux...)
		for name := range oracle {
			fmt.Fprintf(&sh, "echo 'BEGIN %s oracle %s'\n%s < %s\necho \"END $?\"\n", mode, name, w(drv), w(filepath.Join(dir, name+".in")))
		}
		for _, c := range tlsPairTable {
			l := leaves[c.leaf]
			fmt.Fprintf(&sh, "echo 'BEGIN %s pair %s'\n%s %s %s %s %d %d\necho \"END $?\"\n", mode, c.name, w(pair), w(l[0]), w(l[1]), w(l[2]), c.mode, c.suite)
		}
		for _, c := range tlsLinuxPeers {
			l := leaves[c.leaf]
			roots := w(l[2])
			env := ""
			switch c.roots {
			case "other":
				roots = w(other)
			case "system":
				roots, env = "-", "SSL_CERT_FILE="+w(l[2])+" SSL_CERT_DIR=/nonexistent-mk-tls "
			case "system-other":
				roots, env = "-", "SSL_CERT_FILE="+w(other)+" SSL_CERT_DIR=/nonexistent-mk-tls "
			}
			n := c.n
			if n == 0 {
				n = 5
			}
			m := c.mode
			if m == "" {
				m = "plain"
			}
			fmt.Fprintf(&sh, "echo 'BEGIN %s peer %s'\n%s%s serve %s %s %s %s %s ADDR localhost %s %s %s %s 0 300 %d\necho \"END $?\"\n",
				mode, c.name, env, w(helper), w(l[0]), w(l[1]), dash(c.curves), dash(c.protos), w(peer), roots, dash(c.suites), dash(c.alpn), m, n)
		}
		for _, c := range tlsLinuxServers {
			l := leaves[c.leaf]
			n := c.n
			if n == 0 {
				n = 5
			}
			fmt.Fprintf(&sh, "echo 'BEGIN %s server %s'\n%s%s dial %s %s %s %d %d %s %s %s %s\necho \"END $?\"\n",
				mode, c.name, c.env, w(helper), w(l[2]), dash(c.curves), dash(c.client), c.max, n, w(srv), w(l[0]), w(l[1]), dash(c.protos))
		}
	}
	out := linuxRunScript(t, dir, "tls.sh", sh.String(), 9*time.Minute)
	// Each section: its lines between BEGIN and END.
	sections := map[string][]string{}
	var cur string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "BEGIN "):
			cur = strings.TrimPrefix(line, "BEGIN ")
			sections[cur] = []string{}
		case strings.HasPrefix(line, "END "):
			if line != "END 0" {
				t.Errorf("%s: exit %s", cur, strings.TrimPrefix(line, "END "))
			}
			cur = ""
		case cur != "":
			sections[cur] = append(sections[cur], line)
		}
	}
	for _, mode := range tlsModes {
		for name, cases := range oracle {
			got := sections[mode+" oracle "+name]
			if len(got) != len(cases) {
				t.Errorf("%s oracle %s: %d answers for %d records", mode, name, len(got), len(cases))
				continue
			}
			bad := 0
			for i, c := range cases {
				if got[i] != c.want {
					bad++
					if bad <= 10 {
						t.Errorf("%s oracle %s (%s):\n got %s\nwant %s", mode, name, c.name, got[i], c.want)
					}
				}
			}
			t.Logf("%s oracle %s: %d of %d agree", mode, name, len(cases)-bad, len(cases))
		}
		for _, c := range tlsPairTable {
			got := sections[mode+" pair "+c.name]
			if len(got) != 1 || !tlsLineMatches(got[0], c.want) {
				t.Errorf("%s pair %s: got %q, want %q", mode, c.name, got, c.want)
			}
		}
		for _, c := range tlsLinuxPeers {
			tlsLinuxCheck(t, mode+" peer "+c.name, sections[mode+" peer "+c.name], c.want, c.goWant)
		}
		for _, c := range tlsLinuxServers {
			tlsLinuxCheck(t, mode+" server "+c.name, sections[mode+" server "+c.name], c.want, c.goWant)
		}
	}
}

// tlsLinuxCheck checks a helper section's D and G lines.
func tlsLinuxCheck(t *testing.T, name string, lines []string, want, goWant string) {
	t.Helper()
	var d, g string
	for _, l := range lines {
		if s, ok := strings.CutPrefix(l, "D "); ok {
			d = s
		} else if s, ok := strings.CutPrefix(l, "G "); ok {
			g = s
		}
	}
	if !tlsGlob(d, want) || !tlsGlob(g, goWant) {
		t.Errorf("%s:\n Mk: %q, want %q\n Go: %q, want %q\n%s", name, d, want, g, goWant, strings.Join(lines, "\n"))
	}
}
