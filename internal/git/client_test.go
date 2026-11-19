// Why: the git protocol client talks to servers the user does not control, so
// its requests, side bands and credential handling must be exact.
// What: replay tests over testdata/synthetic and testdata/recorded, the request
// bodies, refusals, side bands, redirects and netrc credentials (TestGit*).
// How: replayHandler serves the stored replies through httptest; credentials go
// over https to their own host and port only and are dropped on a redirect.

package git

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
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

// replayHandler serves a fixture directory's replies under /repo.
func replayHandler(t *testing.T, dir string) http.Handler {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		return b
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Git-Protocol") != "version=2" {
			t.Errorf("%s %s: Git-Protocol is %q", r.Method, r.URL, r.Header.Get("Git-Protocol"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repo/info/refs":
			if r.URL.RawQuery != "service=git-upload-pack" {
				t.Errorf("discovery query %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			w.Write(read("discovery.pkt"))
		case r.Method == http.MethodPost && r.URL.Path == "/repo/git-upload-pack":
			if ct := r.Header.Get("Content-Type"); ct != "application/x-git-upload-pack-request" {
				t.Errorf("POST Content-Type %q", ct)
			}
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
			switch {
			case bytes.Contains(body, []byte("command=ls-refs\n")):
				w.Write(read("ls-refs.pkt"))
			case bytes.Contains(body, []byte("command=fetch\n")):
				w.Write(read("fetch.pkt"))
			default:
				http.Error(w, "unknown command", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	})
}

// resultText is FetchTag's result in expect.txt's form.
func resultText(tag string, s *Snapshot, err error) string {
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) {
			return "error " + ge.Reason + "\n"
		}
		return "error " + err.Error() + "\n"
	}
	return expectText(tag, s.Commit, s.Files)
}

func replayDir(t *testing.T, dir, tag string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(dir, "expect.txt"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(replayHandler(t, dir))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Agent: "mk/test"}
	s, err := c.FetchTag(srv.URL+"/repo", tag)
	if got := resultText(tag, s, err); got != string(want) {
		t.Errorf("%s:\ngot  %s\nwant %s(err %v)", dir, got, want, err)
	}
}

// TestGitReplaySynthetic replays every synthetic fixture set.
func TestGitReplaySynthetic(t *testing.T) {
	dirs, err := os.ReadDir(syntheticDir)
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no synthetic fixtures (%v); run TestSyntheticFixturesCurrent with -update", err)
	}
	for _, d := range dirs {
		t.Run(d.Name(), func(t *testing.T) { replayDir(t, filepath.Join(syntheticDir, d.Name()), "v1.0.0") })
	}
}

// TestGitReplayRecorded replays the recorded fixtures of a public
// repository (record_test.go, testdata/README.md). It skips until the
// recording is committed.
func TestGitReplayRecorded(t *testing.T) {
	dirs, _ := os.ReadDir(recordedDir)
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		meta, err := os.ReadFile(filepath.Join(recordedDir, d.Name(), "meta.txt"))
		if err != nil {
			t.Fatal(err)
		}
		tag := metaField(string(meta), "tag")
		n++
		t.Run(d.Name(), func(t *testing.T) { replayDir(t, filepath.Join(recordedDir, d.Name()), tag) })
	}
	if n == 0 {
		t.Skip("no recorded fixtures under testdata/recorded yet (MK_NET=1 MK_GIT_RECORD=1, testdata/README.md)")
	}
}

func metaField(meta, key string) string {
	for line := range strings.SplitSeq(meta, "\n") {
		if v, ok := strings.CutPrefix(line, key+" "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TestGitRequestBodies checks the discovery, ls-refs and fetch requests
// byte for byte.
func TestGitRequestBodies(t *testing.T) {
	dir := filepath.Join(syntheticDir, "ok-annotated")
	var mu sync.Mutex
	var bodies []string
	inner := replayHandler(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Agent: "mk/0.1.0"}
	s, err := c.FetchTag(srv.URL+"/repo", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	lsRefs := "0014command=ls-refs\n" + "0013agent=mk/0.1.0\n" + "0017object-format=sha1\n" + "0001" +
		"0009peel\n" + "001bref-prefix refs/tags/v\n" + "0000"
	fetch := "0012command=fetch\n" + "0013agent=mk/0.1.0\n" + "0017object-format=sha1\n" + "0001" +
		"0032want " + s.Ref.OID.String() + "\n" + "000ddeepen 1\n" + "000eofs-delta\n" + "0010no-progress\n" + "0009done\n" + "0000"
	if len(bodies) != 2 || bodies[0] != lsRefs || bodies[1] != fetch {
		t.Errorf("request bodies:\n%q\nwant\n%q\n%q", bodies, lsRefs, fetch)
	}
}

// TestGitRefusals: no version 2, SHA-256, a dumb server.
func TestGitRefusals(t *testing.T) {
	for name, body := range map[string][]byte{
		"no-version-2": v0Advertisement(),
		"sha256":       discoveryReply(true, "ls-refs", "fetch", "object-format=sha256"),
		"no-fetch":     discoveryReply(true, "ls-refs"),
	} {
		caps, err := ParseDiscovery(body)
		var ge *Error
		if !errors.As(err, &ge) || ge.Reason != ReasonProtocol || !strings.Contains(ge.Msg, "url dependency") {
			t.Errorf("%s: %v, %v; want a protocol refusal naming the url form", name, caps, err)
		}
	}
	// A server that is not smart HTTP answers text/html.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	_, err := (&Client{HTTP: srv.Client()}).Discover(srv.URL + "/repo")
	var ge *Error
	if !errors.As(err, &ge) || ge.Reason != ReasonProtocol {
		t.Errorf("dumb server: %v", err)
	}
}

// TestGitSideBands: band 2 ignored, band 3 an error with its text.
func TestGitSideBands(t *testing.T) {
	w := &PktWriter{}
	w.Line("packfile")
	w.Data([]byte("\x02progress"))
	w.Data([]byte("\x01PA"))
	w.Data([]byte("\x01CK"))
	w.Flush()
	got, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100)
	if err != nil || string(got) != "PACK" {
		t.Errorf("band 1: %q, %v", got, err)
	}
	w = &PktWriter{}
	w.Line("packfile")
	w.Data([]byte("\x03upload-pack: not our ref"))
	w.Flush()
	_, err = ReadFetchReply(bytes.NewReader(w.Bytes()), 100)
	if err == nil || !strings.Contains(err.Error(), "not our ref") {
		t.Errorf("band 3: %v", err)
	}
	w = &PktWriter{}
	w.Line("ERR access denied")
	if _, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("ERR: %v", err)
	}
	w = &PktWriter{}
	w.Line("packfile")
	w.Data([]byte("\x01" + strings.Repeat("x", 200)))
	w.Flush()
	if _, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100); !isReason(err, ReasonLimit) {
		t.Errorf("pack over its cap: %v", err)
	}
}

func isReason(err error, reason string) bool {
	var ge *Error
	return errors.As(err, &ge) && ge.Reason == reason
}

// tlsPair starts TLS servers reached through host names: the client dials
// every name to its server, whatever the name, so redirects between host
// names can be tested without DNS.
type tlsHosts struct {
	srv  map[string]*httptest.Server
	auth map[string][]string // host -> Authorization headers seen
	mu   sync.Mutex
}

func newTLSHosts(t *testing.T, handlers map[string]http.HandlerFunc) (*tlsHosts, *http.Client) {
	h := &tlsHosts{srv: map[string]*httptest.Server{}, auth: map[string][]string{}}
	for name, fn := range handlers {
		name, fn := name, fn
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.mu.Lock()
			h.auth[name] = append(h.auth[name], r.Header.Get("Authorization"))
			h.mu.Unlock()
			fn(w, r)
		}))
		t.Cleanup(s.Close)
		h.srv[name] = s
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test servers' own certificate
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			s, ok := h.srv[host]
			if !ok {
				return nil, errors.New("no test host " + host)
			}
			return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return h, &http.Client{Transport: tr}
}

func (h *tlsHosts) seen(host string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.auth[host]...)
}

func basic(login, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(login+":"+pass))
}

func advertise(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	w.Write(discoveryReply(true, stdCaps...))
}

// TestGitRedirectDropsAuthorization: netrc names a.example; a redirect
// to b.example (another host), to sub.a.example (a subdomain, which Go's
// own rule would keep the header for) or to a.example on another port
// carries no Authorization; a redirect within a.example's host and port
// keeps it.
func TestGitRedirectDropsAuthorization(t *testing.T) {
	netrc := ParseNetrc([]byte("machine a.example login alice password s3cret\n"))
	for _, tc := range []struct {
		name, to string
		keep     bool
	}{
		{"other-host", "https://b.example:443/repo/info/refs?service=git-upload-pack", false},
		{"subdomain", "https://sub.a.example:443/repo/info/refs?service=git-upload-pack", false},
		{"other-port", "https://a.example:8443/repo/info/refs?service=git-upload-pack", false},
		{"same-host", "/moved/info/refs?service=git-upload-pack", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hosts, hc := newTLSHosts(t, map[string]http.HandlerFunc{
				"a.example": func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/moved/") || r.Host == "a.example:8443" {
						advertise(w, r)
						return
					}
					http.Redirect(w, r, tc.to, http.StatusFound)
				},
				"b.example":     advertise,
				"sub.a.example": advertise,
			})
			c := &Client{HTTP: hc, Netrc: netrc}
			r, err := c.Discover("https://a.example:443/repo")
			if err != nil {
				t.Fatal(err)
			}
			want := basic("alice", "s3cret")
			var all []string
			for _, h := range []string{"a.example", "b.example", "sub.a.example"} {
				all = append(all, hosts.seen(h)...)
			}
			if len(all) != 2 || all[0] != want {
				t.Fatalf("Authorization headers %q; want the first request to carry it", all)
			}
			if got := all[1] == want; got != tc.keep {
				t.Errorf("after the redirect to %s: Authorization %q (keep %v); base %s", tc.to, all[1], tc.keep, r.URL())
			}
		})
	}
}

// TestGitRedirectToHTTPRefused: a redirect from https to plain http
// elsewhere is refused, so a password never leaves over http.
func TestGitRedirectToHTTPRefused(t *testing.T) {
	_, hc := newTLSHosts(t, map[string]http.HandlerFunc{
		"a.example": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://a.example/repo/info/refs?service=git-upload-pack", http.StatusFound)
		},
	})
	c := &Client{HTTP: hc, Netrc: ParseNetrc([]byte("machine a.example login alice password s3cret"))}
	_, err := c.Discover("https://a.example/repo")
	if !isReason(err, ReasonNetwork) || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("https -> http redirect: %v", err)
	}
}

// TestGitNoCredentialsOverHTTP: a loopback http server named in the
// netrc gets no Authorization.
func TestGitNoCredentialsOverHTTP(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		advertise(w, r)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Netrc: ParseNetrc([]byte("machine 127.0.0.1 login alice password s3cret"))}
	if _, err := c.Discover(srv.URL + "/repo"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization over http: %q", got)
	}
}

// TestGitPostNotRedirected: a POST answered with a redirect fails
// rather than being replayed as a GET elsewhere.
func TestGitPostNotRedirected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			advertise(w, r)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client()}
	r, err := c.Discover(srv.URL + "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.LsRefs(r, "refs/tags/v"); !isReason(err, ReasonNetwork) {
		t.Errorf("redirected POST: %v", err)
	}
}

// TestGitCredentialsInNoMessage: a URL with user info is refused
// without repeating the password, and a 401 names netrc, not a password.
func TestGitCredentialsInNoMessage(t *testing.T) {
	err := CheckURL("https://alice:s3cret@example.com/repo")
	if !isReason(err, ReasonInvalid) || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("user info: %v", err)
	}
	_, hc := newTLSHosts(t, map[string]http.HandlerFunc{
		"a.example": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) },
	})
	c := &Client{HTTP: hc, Netrc: ParseNetrc([]byte("machine a.example login alice password s3cret"))}
	_, err = c.Discover("https://a.example/repo")
	if err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "netrc") {
		t.Errorf("401: %v", err)
	}
}

func TestGitNetrc(t *testing.T) {
	n := ParseNetrc([]byte(`# comment
machine Example.COM login alice password one
macdef init
machine evil.example login x password y

default login anyone password everywhere
machine example.com login second password two
machine git.example
  login bob
  password pw
`))
	if l, p, ok := n.Lookup("example.com:443"); !ok || l != "alice" || p != "one" {
		t.Errorf("example.com: %q %q %v", l, p, ok)
	}
	if _, _, ok := n.Lookup("evil.example"); ok {
		t.Error("a machine inside a macdef body was read")
	}
	if _, _, ok := n.Lookup("other.example"); ok {
		t.Error("the default entry was used")
	}
	if l, _, ok := n.Lookup("git.example"); !ok || l != "bob" {
		t.Errorf("git.example: %q %v", l, ok)
	}
	dir := t.TempDir()
	env := map[string]string{"NETRC": filepath.Join(dir, "n")}
	get := func(k string) string { return env[k] }
	if p := NetrcPath(get, true); p != env["NETRC"] {
		t.Errorf("NETRC: %s", p)
	}
	env = map[string]string{"USERPROFILE": dir}
	if p := NetrcPath(get, true); p != filepath.Join(dir, "_netrc") {
		t.Errorf("windows without .netrc: %s", p)
	}
	os.WriteFile(filepath.Join(dir, ".netrc"), []byte("machine h login l password p"), 0o600)
	if p := NetrcPath(get, true); p != filepath.Join(dir, ".netrc") {
		t.Errorf("windows with .netrc: %s", p)
	}
	env = map[string]string{"HOME": dir}
	if p := NetrcPath(get, false); p != filepath.Join(dir, ".netrc") {
		t.Errorf("unix: %s", p)
	}
	if n, err := LoadNetrc(filepath.Join(dir, "missing")); err != nil || n == nil {
		t.Errorf("missing netrc: %v", err)
	}
	if n, err := LoadNetrc(filepath.Join(dir, ".netrc")); err != nil {
		t.Error(err)
	} else if _, p, _ := n.Lookup("h"); p != "p" {
		t.Errorf("loaded: %q", p)
	}
}
