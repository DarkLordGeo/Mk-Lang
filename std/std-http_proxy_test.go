// Why: ClientLimits.proxy must send http URLs in absolute form and tunnel
// https through CONNECT, verifying the origin through the tunnel.
// What: TestStdHttp_ProxyAgainstGo, a Mk program fetching through a Go
// forward proxy on loopback.
// How: the origin's name, example.test, resolves nowhere and only the proxy
// maps it, so an arriving request shows the client never resolved it.

package std

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// http2ProxyProg: args are the proxy's URL, a PEM file of roots, then the
// URLs to fetch in order on one client. It prints "got STATUS BODY" or
// "error E" for each.
const http2ProxyProg = `import std.fs
import std.http
import std.tls
import std.url
import std.x509

export Main(sys: &System): http.Error|url.Error|x509.Error|fs.Error|WriteError!void {
    var out := sys.Stdout()
    a := sys.arena
    io := sys.io
    p := try url.Parse(a, sys.args[1], none)
    text := try fs.ReadText(a, io, sys.args[2])
    certs := try x509.ParsePem(a, text)
    all := certs.Done()
    pool := x509.Pool.New(all[..])
    var cfg := tls.DefaultConfig
    cfg.roots = some(&pool)
    var l := http.DefaultClientLimits
    l.proxy = some(&p)
    l.secure = some(tls.Connector(&cfg))
    c := http.Client.New(sys.heap, l)
    defer c.Close()
    hs: [2]http.Header = [http.Header(name: "Proxy-Authorization", value: "Basic eDp5"), http.Header(name: "X-Kind", value: "proxy")]
    for i in 3..sys.args.Len() {
        u := try url.Parse(a, sys.args[i], none)
        req := http.ClientRequest(method: "GET", url: &u, headers: hs[..], body: "".AsBytes())
        reply := c.Fetch(a, io, &req) else e {
            try Print(mut out, "error {e}\n", (e))
            try out.Flush()
            continue
        }
        try Print(mut out, "got {s} ", (s: reply.status))
        try out.Bytes(reply.body)
        try out.Str("\n")
        try out.Flush()
    }
}
`

// TestStdHttp_ProxyAgainstGo: an http URL arrives in absolute form, which
// the proxy forwards with Go's Transport; an https URL arrives as a CONNECT to
// host:port, which the proxy answers 200 and relays, and std.tls verifies the
// origin's certificate (for example.test, from tlsPeerPKI's root) through the
// tunnel. A CONNECT the proxy answers 407 with an error page is ProxyRefused.
func TestStdHttp_ProxyAgainstGo(t *testing.T) {
	pki := tlsPeerPKI(t)
	exe := http2Build(t, http2ProxyProg)
	var mu sync.Mutex
	var log []string
	note := func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, fmt.Sprintf(f, a...))
	}
	seen := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(log, "\n")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		note("origin %s %s host=%s pa=%q kind=%q tls=%v", r.Method, r.RequestURI, r.Host, r.Header.Get("Proxy-Authorization"), r.Header.Get("X-Kind"), r.TLS != nil)
		fmt.Fprintf(w, "origin %s", r.URL.Path)
	})
	secure := httptest.NewUnstartedServer(handler)
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}}
	secure.StartTLS()
	defer secure.Close()
	plain := httptest.NewServer(handler)
	defer plain.Close()
	// example.test is loopback to the proxy alone.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		h, p, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if h == "example.test" {
			addr = net.JoinHostPort("127.0.0.1", p)
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	fwd := &http.Transport{DialContext: dial}
	defer fwd.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			note("connect %s host=%s pa=%q", r.RequestURI, r.Host, r.Header.Get("Proxy-Authorization"))
			if strings.HasPrefix(r.Host, "refuse.test") {
				// With an error page: the status decides, not the body.
				http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
				return
			}
			up, err := dial(r.Context(), "tcp", r.Host)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			conn, bw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				up.Close()
				return
			}
			bw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
			bw.Flush()
			done := make(chan struct{})
			go func() {
				io.Copy(up, bw)
				up.Close()
				close(done)
			}()
			io.Copy(conn, up)
			conn.Close()
			<-done
			return
		}
		note("forward %s %s abs=%v host=%s pa=%q", r.Method, r.RequestURI, r.URL.IsAbs(), r.Host, r.Header.Get("Proxy-Authorization"))
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header.Del("Proxy-Authorization")
		resp, err := fwd.RoundTrip(out)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	roots := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(roots, []byte(pki.rootPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	_, pport, _ := net.SplitHostPort(plain.Listener.Addr().String())
	_, sport, _ := net.SplitHostPort(secure.Listener.Addr().String())
	pu := "http://example.test:" + pport
	su := "https://example.test:" + sport
	r := runNative(t, exe, "", proxy.URL, roots, pu+"/plain", pu+"/plain", su+"/secure", su+"/secure", "https://refuse.test/x")
	out := r.stdout + r.stderr
	http2Want(t, out, []string{"got 200 origin /plain", "got 200 origin /plain", "got 200 origin /secure", "got 200 origin /secure", "error ProxyRefused(code: 407)"})
	all := seen()
	for _, want := range []string{
		// Absolute form, Host, and the caller's Proxy-Authorization, which
		// the proxy consumes.
		fmt.Sprintf(`forward GET http://example.test:%s/plain abs=true host=example.test:%s pa="Basic eDp5"`, pport, pport),
		fmt.Sprintf(`origin GET /plain host=example.test:%s pa="" kind="proxy" tls=false`, pport),
		// CONNECT to host:port with that Host and no credentials; inside
		// the tunnel, origin form and no Proxy-Authorization.
		fmt.Sprintf(`connect example.test:%s host=example.test:%s pa=""`, sport, sport),
		fmt.Sprintf(`origin GET /secure host=example.test:%s pa="" kind="proxy" tls=true`, sport),
		`connect refuse.test:443 host=refuse.test:443 pa=""`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the proxy and origin never saw %q; they saw:\n%s\nMk printed:\n%s", want, all, out)
		}
	}
	// The second https request reused the first one's tunnel.
	if n := strings.Count(all, "connect example.test:"); n != 1 {
		t.Errorf("%d CONNECTs to example.test, want 1 (the tunnel pooled):\n%s", n, all)
	}
}
