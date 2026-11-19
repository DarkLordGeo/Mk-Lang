// Why: a git tag dependency must give the same tree, hash and refusals as an
// archive of the same files.
// What: TestGit* tests of the tree hash, tag replay, archive and git alike,
// refusals and the verified write.
// How: the git server is httptest replaying internal/git's synthetic fixtures;
// the hash is checked before anything is written.

package fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mk/internal/git"
)

const gitFixtures = "../git/testdata/synthetic"

// gitServer serves one of internal/git's fixture sets under /repo.
func gitServer(t *testing.T, set string) *httptest.Server {
	t.Helper()
	dir := filepath.Join(gitFixtures, set)
	read := func(name string) []byte {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repo/info/refs":
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			w.Write(read("discovery.pkt"))
		case r.Method == http.MethodPost && r.URL.Path == "/repo/git-upload-pack":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
			if bytes.Contains(body, []byte("command=ls-refs\n")) {
				w.Write(read("ls-refs.pkt"))
			} else {
				w.Write(read("fetch.pkt"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// expectedTreeHash computes the tree hash from a fixture's expect.txt
// ("file <mode> <sha256> <path>" lines), independently of Tree.Hash.
func expectedTreeHash(t *testing.T, set string) string {
	b, err := os.ReadFile(filepath.Join(gitFixtures, set, "expect.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for l := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) == 4 && f[0] == "file" {
			lines = append(lines, f[2]+"  "+f[3]+"\n")
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		return strings.SplitN(lines[i], "  ", 2)[1] < strings.SplitN(lines[j], "  ", 2)[1]
	})
	s := sha256.Sum256([]byte(strings.Join(lines, "")))
	return "tree-sha256:" + hex.EncodeToString(s[:])
}

func noNetrc() *git.Netrc { return git.ParseNetrc(nil) }

// TestGitTreeHash: the formula, by hand, and its independence from
// file order; the empty tree hashes the empty string.
func TestGitTreeHash(t *testing.T) {
	a, b := sha256.Sum256([]byte("A\n")), sha256.Sum256([]byte("B\n"))
	text := hex.EncodeToString(b[:]) + "  Z/b.txt\n" + hex.EncodeToString(a[:]) + "  a.txt\n"
	want := sha256.Sum256([]byte(text)) // "Z" (0x5a) sorts before "a" (0x61): byte order
	tr := &Tree{Files: []File{{Path: "a.txt", Data: []byte("A\n")}, {Path: "Z/b.txt", Data: []byte("B\n")}}}
	if got := tr.Hash(); got != TreeHash(want) {
		t.Errorf("hash %s, want %x", got, want)
	}
	if got := tr.Hash().String(); !strings.HasPrefix(got, "tree-sha256:") || len(got) != 12+64 {
		t.Errorf("spelling %s", got)
	}
	if h, ok := ParseTreeHash(strings.ToUpper(tr.Hash().String()[12:])); ok {
		t.Errorf("no prefix parsed: %s", h)
	}
	if h, ok := ParseTreeHash("tree-sha256:" + strings.ToUpper(tr.Hash().String()[12:])); !ok || h != tr.Hash() {
		t.Errorf("uppercase round trip")
	}
	empty := sha256.Sum256(nil)
	if got := (&Tree{}).Hash(); got != TreeHash(empty) {
		t.Errorf("empty tree %s", got)
	}
}

// TestGitTagReplay fetches the synthetic tag through httptest; its
// hash matches the one computed from the fixture's file list.
func TestGitTagReplay(t *testing.T) {
	for _, set := range []string{"ok-annotated", "ok-lightweight"} {
		srv := gitServer(t, set)
		f := &Fetcher{Client: srv.Client(), Netrc: noNetrc()}
		gt, err := f.GitTag(srv.URL+"/repo", "v1.0.0")
		if err != nil {
			t.Fatalf("%s: %v", set, err)
		}
		if got, want := gt.Hash.String(), expectedTreeHash(t, set); got != want {
			t.Errorf("%s: %s, want %s", set, got, want)
		}
		if len(gt.Commit) != 40 || len(gt.Tree.Files) != 7 || gt.Tree.Files[0].Path != "README.md" {
			t.Errorf("%s: commit %s, files %v", set, gt.Commit, paths(gt.Tree))
		}
	}
}

// TestGitArchiveAndGitAlike: the same files as a .tar.gz with a top
// directory hash the same as the git tag.
func TestGitArchiveAndGitAlike(t *testing.T) {
	srv := gitServer(t, "ok-annotated")
	gt, err := (&Fetcher{Client: srv.Client(), Netrc: noNetrc()}).GitTag(srv.URL+"/repo", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	var es []entry
	for _, f := range gt.Tree.Files {
		es = append(es, entry{name: "demo-1.0.0/" + f.Path, body: string(f.Data)})
	}
	tr, err := Unpack(tarGz(t, es), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Hash() != gt.Hash {
		t.Errorf("archive %s, git %s", tr.Hash(), gt.Hash)
	}
}

// TestGitRefusals: each fault of the fetch is its E0202 reason.
func TestGitRefusals(t *testing.T) {
	for set, want := range map[string]string{
		"link":        ReasonUnsafeArchive,
		"submodule":   ReasonUnsafeArchive,
		"case-clash":  ReasonUnsafeArchive,
		"sha256":      ReasonProtocol,
		"protocol-v0": ReasonProtocol,
	} {
		srv := gitServer(t, set)
		_, err := (&Fetcher{Client: srv.Client(), Netrc: noNetrc()}).GitTag(srv.URL+"/repo", "v1.0.0")
		if got := reason(err); got != want {
			t.Errorf("%s: %q (%v), want %s", set, got, err, want)
		}
	}
	srv := gitServer(t, "ok-annotated")
	f := &Fetcher{Client: srv.Client(), Netrc: noNetrc()}
	if _, err := f.GitTag(srv.URL+"/repo", "v9.9.9"); reason(err) != ReasonNoVersion {
		t.Errorf("a missing tag: %v", err)
	}
	if _, err := (&Fetcher{Client: srv.Client(), Netrc: noNetrc(), MaxArchive: 100}).GitTag(srv.URL+"/repo", "v1.0.0"); reason(err) != ReasonFetch || !strings.Contains(err.Error(), "100 bytes") {
		t.Errorf("over the byte limit: %v", err)
	}
	if _, err := (&Fetcher{Offline: true}).GitTag(srv.URL+"/repo", "v1.0.0"); reason(err) != ReasonOffline {
		t.Errorf("offline: %v", err)
	}
	if _, err := f.GitTag("https://alice:s3cret@example.com/repo", "v1.0.0"); reason(err) != ReasonInvalid || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("credentials in the URL: %v", err)
	}
	refs, err := f.GitTags(srv.URL + "/repo")
	if err != nil || len(refs) != 2 || refs[1].Name != "refs/tags/v1.0.0" {
		t.Errorf("tags: %v %v", refs, err)
	}
}

// TestGitFromGitRefusals: a git tree gets CheckPath and the limits, and
// keeps a lone top directory.
func TestGitFromGitRefusals(t *testing.T) {
	ok, err := FromGit([]git.File{{Path: "only/a.txt", Data: []byte("a")}}, Limits{})
	if err != nil || len(ok.Files) != 1 || ok.Files[0].Path != "only/a.txt" {
		t.Errorf("lone top directory: %v %v", ok, err)
	}
	for name, tc := range map[string]struct {
		files []git.File
		lim   Limits
	}{
		"device":    {[]git.File{{Path: "src/con.txt"}}, Limits{}},
		"colon":     {[]git.File{{Path: "a:b"}}, Limits{}},
		"case":      {[]git.File{{Path: "A"}, {Path: "a"}}, Limits{}},
		"file-dir":  {[]git.File{{Path: "a"}, {Path: "a/b"}}, Limits{}},
		"files":     {[]git.File{{Path: "a"}, {Path: "b"}}, Limits{MaxFiles: 1}},
		"bytes":     {[]git.File{{Path: "a", Data: []byte("12345")}}, Limits{MaxBytes: 4}},
		"trail dot": {[]git.File{{Path: "a./b"}}, Limits{}},
	} {
		if _, err := FromGit(tc.files, tc.lim); reason(err) != ReasonUnsafeArchive {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestGitWriteVerified: a wrong hash writes nothing; the right one
// writes the tree.
func TestGitWriteVerified(t *testing.T) {
	tr := &Tree{Files: []File{{Path: "a/b.txt", Data: []byte("hi\n")}}}
	dir := filepath.Join(t.TempDir(), "out")
	var wrong TreeHash
	err := tr.WriteVerified(dir, wrong)
	if reason(err) != ReasonSumMismatch || !strings.Contains(err.Error(), tr.Hash().String()) || !strings.Contains(err.Error(), wrong.String()) {
		t.Errorf("mismatch: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("a mismatched tree reached the disk: %v", err)
	}
	if err := tr.WriteVerified(dir, tr.Hash()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a", "b.txt")); err != nil || string(b) != "hi\n" {
		t.Errorf("written: %q %v", b, err)
	}
}
