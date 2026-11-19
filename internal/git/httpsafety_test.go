// Why: redirects, credentials, server text and pack sizes are where a hostile
// server attacks a git client.
// What: TestNoRedirectToLoopbackHTTP, TestPostsKeepTheOriginalHost, TestErrLinesEverywhere, TestNetrcQuotes, TestSizeOverflow.
// How: httptest servers try redirects to local http, foreign credentials, ERR
// lines, quoted netrc values and a pack size varint over 2^64.

package git

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestNoRedirectToLoopbackHTTP: https://a.example redirecting to
// http://127.0.0.1 is refused as a network fault.
func TestNoRedirectToLoopbackHTTP(t *testing.T) {
	_, hc := newTLSHosts(t, map[string]http.HandlerFunc{
		"a.example": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:8080/admin/info/refs?service=git-upload-pack", http.StatusFound)
		},
	})
	_, err := (&Client{HTTP: hc}).Discover("https://a.example/repo")
	if !isReason(err, ReasonNetwork) {
		t.Errorf("remote -> http loopback: %v", err)
	}
}

// TestPostsKeepTheOriginalHost: discovery at a.example:443 redirects
// to a.example:8443; the redirected GET and the ls-refs POST there carry
// no Authorization, though the netrc names a.example.
func TestPostsKeepTheOriginalHost(t *testing.T) {
	type seen struct{ method, host, auth string }
	var mu sync.Mutex
	var log []seen
	_, hc := newTLSHosts(t, map[string]http.HandlerFunc{
		"a.example": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			log = append(log, seen{r.Method, r.Host, r.Header.Get("Authorization")})
			mu.Unlock()
			switch {
			case r.Host == "a.example:443" || r.Host == "a.example":
				http.Redirect(w, r, "https://a.example:8443/repo/info/refs?service=git-upload-pack", http.StatusFound)
			case r.Method == http.MethodGet:
				advertise(w, r)
			default:
				io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
				w.Write(lsRefsReply(strings.Repeat("a", 40) + " refs/tags/v1.0.0"))
			}
		},
	})
	c := &Client{HTTP: hc, Netrc: ParseNetrc([]byte("machine a.example login alice password s3cret"))}
	r, err := c.Discover("https://a.example:443/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.LsRefs(r, "refs/tags/v"); err != nil {
		t.Fatal(err)
	}
	if len(log) != 3 || log[0].auth != basic("alice", "s3cret") || log[1].auth != "" || log[2].method != http.MethodPost || log[2].auth != "" {
		t.Errorf("requests %+v; want credentials on the first only", log)
	}
}

// TestErrLinesEverywhere: ERR in discovery, in shallow-info and in the
// packfile section is a network fault with the server's text; band 3 and
// ERR text are sanitised and capped.
func TestErrLinesEverywhere(t *testing.T) {
	w := &PktWriter{}
	w.Line("ERR repository not found")
	if _, err := ParseDiscovery(w.Bytes()); !isReason(err, ReasonNetwork) || !strings.Contains(err.Error(), "repository not found") {
		t.Errorf("discovery: %v", err)
	}
	w = &PktWriter{}
	w.Line("shallow-info")
	w.Line("ERR shallow refused")
	if _, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100); !isReason(err, ReasonNetwork) || !strings.Contains(err.Error(), "shallow refused") {
		t.Errorf("shallow-info: %v", err)
	}
	w = &PktWriter{}
	w.Line("packfile")
	w.Line("ERR pack failed")
	if _, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100); !isReason(err, ReasonNetwork) || !strings.Contains(err.Error(), "pack failed") {
		t.Errorf("packfile: %v", err)
	}
	w = &PktWriter{}
	w.Line("packfile")
	w.Data([]byte("\x03\x1b[2J" + strings.Repeat("z", 1000)))
	_, err := ReadFetchReply(bytes.NewReader(w.Bytes()), 100)
	if !isReason(err, ReasonNetwork) || strings.ContainsRune(err.Error(), 0x1b) || len(err.Error()) > 300 {
		t.Errorf("band 3: %d bytes, %.60q", len(err.Error()), err)
	}
}

func TestNetrcQuotes(t *testing.T) {
	n := ParseNetrc([]byte(`machine a.example login "al ice" password "has space \"and\" \\ quote"
machine b.example login bob password plain`))
	if l, p, ok := n.Lookup("a.example"); !ok || l != "al ice" || p != `has space "and" \ quote` {
		t.Errorf("quoted: %q %q %v", l, p, ok)
	}
	if _, p, ok := n.Lookup("b.example"); !ok || p != "plain" {
		t.Errorf("after a quoted line: %q %v", p, ok)
	}
}

// TestSizeOverflow: a declared size of 2^64 + 1 is refused, not wrapped.
func TestSizeOverflow(t *testing.T) {
	body := []byte("PACK\x00\x00\x00\x02\x00\x00\x00\x01")
	// type blob, size bits: 1 in the low nibble, then continuation bytes
	// that put a 1 at bit 64.
	body = append(body, 0x80|byte(ObjBlob)<<4|1, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x10)
	body = append(body, zlibBytes([]byte("a"))...)
	if _, err := ParsePack(withTrailer(body), Limits{}); !isReason(err, ReasonCorrupt) || !strings.Contains(err.Error(), "2^64") {
		t.Errorf("size 2^64+1: %v", err)
	}
}
