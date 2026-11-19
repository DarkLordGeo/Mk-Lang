// Why: synthetic fixtures can drift from what a real git server sends.
// What: TestGitRecord and TestGitLive, both run only with MK_NET=1.
// How: Record (also MK_GIT_RECORD=1) writes one real tag's replies under
// testdata/recorded/ for replay; Live compares a fresh fetch with them.
// testdata/README.md says how to record; TestGitReplayRecorded replays it.

package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const recordedDir = "testdata/recorded"

// The repository recorded unless MK_GIT_RECORD_URL and MK_GIT_RECORD_TAG
// name another: small, archived (so its tag cannot move). When its tag
// turns out lightweight, the annotated target below is recorded as well
// (MK_GIT_RECORD_URL2 and MK_GIT_RECORD_TAG2 name another), so a fetch
// whose want is a tag object is recorded too. The fallback named here,
// git's own repository, annotates every release tag but is large and its
// tree holds a link; a second target, if one is ever recorded, should be a
// small repository with annotated tags. The checked-in fixtures are
// synthetic only: none of this runs by default.
const (
	defaultRecordURL  = "https://github.com/pkg/errors"
	defaultRecordTag  = "v0.9.1"
	defaultRecordURL2 = "https://github.com/git/git"
	defaultRecordTag2 = "v2.0.0"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func recordTarget() (string, string) {
	return envOr("MK_GIT_RECORD_URL", defaultRecordURL), envOr("MK_GIT_RECORD_TAG", defaultRecordTag)
}

// recorder keeps the body and Content-Type of each reply, keyed by what
// it answers.
type recorder struct {
	inner   http.RoundTripper
	mu      sync.Mutex
	replies map[string][]byte
	types   map[string]string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := r.inner.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	key := ""
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/info/refs"):
		key = "discovery.pkt"
	case bytes.Contains(reqBody, []byte("command=ls-refs\n")):
		key = "ls-refs.pkt"
	case bytes.Contains(reqBody, []byte("command=fetch\n")):
		key = "fetch.pkt"
	}
	if key != "" {
		r.mu.Lock()
		r.replies[key] = body
		r.types[key] = resp.Header.Get("Content-Type")
		r.mu.Unlock()
	}
	return resp, nil
}

// record fetches one tag through a recorder and writes its fixture set;
// it reports whether the tag is annotated (ls-refs gave a peeled id).
func record(t *testing.T, rawURL, tag string) bool {
	rec := &recorder{inner: http.DefaultTransport, replies: map[string][]byte{}, types: map[string]string{}}
	c := &Client{HTTP: &http.Client{Transport: rec, Timeout: 5 * time.Minute}, Agent: "mk/record"}
	s, err := c.FetchTag(rawURL, tag)
	name := path.Base(strings.TrimSuffix(rawURL, "/")) + "-" + tag
	complete := len(rec.replies) == 3
	var ge *Error
	// A refusal of the tree (a link, a submodule) after a whole fetch is a
	// fixture too; anything else keeps the replies outside the fixtures,
	// so the failure can be read offline without a second download.
	if err != nil && !(complete && errors.As(err, &ge) && ge.Reason == ReasonRefused) {
		fail := filepath.Join(os.Getenv("MK_GIT_RECORD_FAILED"), "failed-"+name)
		if os.Getenv("MK_GIT_RECORD_FAILED") == "" {
			fail = filepath.Join(t.TempDir(), "failed-"+name)
		}
		if mkErr := os.MkdirAll(fail, 0o755); mkErr == nil {
			for k, b := range rec.replies {
				os.WriteFile(filepath.Join(fail, k), b, 0o644)
			}
		}
		t.Fatalf("%s %s: %v (the replies received are in %s; set MK_GIT_RECORD_FAILED to keep them)", rawURL, tag, err, fail)
	}
	annotated := false
	if refs, perr := ParseLsRefs(rec.replies["ls-refs.pkt"]); perr == nil {
		for _, r := range refs {
			if r.Name == "refs/tags/"+tag {
				annotated = !r.Peeled.IsZero()
			}
		}
	}
	dir := filepath.Join(recordedDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The fixture's server path is /repo, so the replay handler serves it.
	meta := fmt.Sprintf("url %s\ntag %s\nannotated %v\nrecorded %s\nby TestGitRecord, go %s, %s/%s\n",
		rawURL, tag, annotated, time.Now().UTC().Format(time.RFC3339), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	for _, k := range []string{"discovery.pkt", "ls-refs.pkt", "fetch.pkt"} {
		meta += fmt.Sprintf("content-type %s %s\n", k, rec.types[k])
	}
	files := map[string][]byte{
		"meta.txt":   []byte(meta),
		"expect.txt": []byte(resultText(tag, s, err)),
	}
	for _, k := range []string{"discovery.pkt", "ls-refs.pkt", "fetch.pkt"} {
		b, ok := rec.replies[k]
		if !ok {
			t.Fatalf("no %s was recorded", k)
		}
		files[k] = b
	}
	for k, b := range files {
		if err := os.WriteFile(filepath.Join(dir, k), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("recorded %s %s (annotated %v, result %q) into %s", rawURL, tag, annotated, strings.SplitN(string(files["expect.txt"]), "\n", 3)[:2], dir)
	return annotated
}

// TestGitRecord records the fixtures. Not run by default: it downloads.
func TestGitRecord(t *testing.T) {
	if os.Getenv("MK_NET") != "1" || os.Getenv("MK_GIT_RECORD") != "1" {
		t.Skip("records from the network only with MK_NET=1 and MK_GIT_RECORD=1 (testdata/README.md)")
	}
	rawURL, tag := recordTarget()
	if !record(t, rawURL, tag) {
		record(t, envOr("MK_GIT_RECORD_URL2", defaultRecordURL2), envOr("MK_GIT_RECORD_TAG2", defaultRecordTag2))
	}
}

// TestGitLive fetches a tag of a public repository over the network.
func TestGitLive(t *testing.T) {
	if os.Getenv("MK_NET") != "1" {
		t.Skip("the live test runs only with MK_NET=1")
	}
	rawURL, tag := recordTarget()
	c := &Client{Agent: "mk/test"}
	s, err := c.FetchTag(rawURL, tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) == 0 {
		t.Fatalf("%s %s: no files", rawURL, tag)
	}
	name := path.Base(strings.TrimSuffix(rawURL, "/")) + "-" + tag
	if want, err := os.ReadFile(filepath.Join(recordedDir, name, "expect.txt")); err == nil {
		if got := expectText(tag, s.Commit, s.Files); got != string(want) {
			t.Errorf("the live fetch differs from the recording (has the tag moved?)\n%s", got)
		}
	}
}
