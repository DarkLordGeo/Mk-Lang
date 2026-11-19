// Why: url dependencies' hashes moved from the manifest into pkg.sum; old
// manifests must keep working while mk fix moves them.
// What: TestSum_URLHashNoteAndSumLine, TestSum_URLWithoutHashCheckedAgainstSum
// and TestSum_FixMovesArchiveHash.
// How: a url Dep's archive hash in the manifest keeps working with a note,
// a url Dep with no hash is checked against pkg.sum, and mk fix's round
// moves the hash into pkg.sum.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSum_URLHashNoteAndSumLine(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, sha(arch))
	r := depCheck(t, app, Config{})
	clean(t, r)
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "mk fix moves it into pkg.sum") {
		t.Errorf("notes %q", r.Notes)
	}
	b, err := os.ReadFile(filepath.Join(app, sumFile))
	if err != nil || !strings.HasPrefix(string(b), url+" - - tree-sha256:") {
		t.Errorf("pkg.sum %q, %v", b, err)
	}
}

func TestSum_URLWithoutHashCheckedAgainstSum(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, "")
	clean(t, depCheck(t, app, Config{})) // first fetch records the line
	good, _ := os.ReadFile(filepath.Join(app, sumFile))
	if !strings.HasPrefix(string(good), url+" - - tree-sha256:") {
		t.Fatalf("pkg.sum %q", good)
	}
	clean(t, depCheck(t, app, Config{}))
	bad := url + " - - tree-sha256:" + strings.Repeat("0", 64) + "\n"
	if err := os.WriteFile(filepath.Join(app, sumFile), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	e0202(t, depCheck(t, app, Config{}), "sum-mismatch", "pkg.mk:")
}

func TestSum_FixMovesArchiveHash(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, sha(arch))
	before := readFile(t, filepath.Join(app, "pkg.mk"))
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Dry run: nothing written.
	round, moved, _, err := s.archiveHashes(true)
	if err != nil || round == nil || len(moved) != 1 || readFile(t, filepath.Join(app, "pkg.mk")) != before {
		t.Fatalf("dry run: %v %+v %+v", err, round, moved)
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Fatal("a dry run wrote pkg.sum")
	}
	round, moved, _, err = s.archiveHashes(false)
	if err != nil || round == nil || len(round.Applied) != 1 {
		t.Fatalf("fix: %v %+v", err, round)
	}
	after := readFile(t, filepath.Join(app, "pkg.mk"))
	if strings.Contains(after, "hash:") || after != strings.Replace(before, `, hash: "`+sha(arch)+`"`, "", 1) {
		t.Errorf("pkg.mk after the fix:\n%s", after)
	}
	sum := readFile(t, filepath.Join(app, sumFile))
	if sum != moved[0].Line+"\n" {
		t.Errorf("pkg.sum %q, want %q", sum, moved[0].Line)
	}
	// The fixed package checks against pkg.sum alone, with no note; a
	// second round has nothing to do.
	r := depCheck(t, app, Config{})
	clean(t, r)
	if len(r.Notes) != 0 {
		t.Errorf("notes %q", r.Notes)
	}
	if round, _, _, err := s.fixArchiveHashes(false); err != nil || round != nil {
		t.Errorf("second round: %v %+v", err, round)
	}
}
