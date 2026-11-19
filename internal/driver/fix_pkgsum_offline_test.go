// Why: mk fix's pkg.sum round moves archive hashes out of manifests; it
// must work offline, keep comments and respect a workspace's pkg.sum.
// What: TestSum_FixOfflineReadsCacheAndSkipsWithNote, TestSum_FixKeepsCommentedDep,
// TestSum_FixUsesWorkspaceSum and TestSum_FixHookRunsRound, over fixHashes.
// How: each case builds a package tree, runs the round and checks the moved
// hashes, the manifest text and the notes.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixHashes runs the round and returns the round, the moves and its notes.
func fixHashes(t *testing.T, s *Session, dryRun bool) (*FixRound, []archiveHashFix, []string, error) {
	t.Helper()
	return s.archiveHashes(dryRun)
}

// An offline mk fix reads the archive from the build's cache, as the build
// does; with no archive and no network the round is skipped with a note.
func TestSum_FixOfflineReadsCacheAndSkipsWithNote(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, sha(arch))
	cache := t.TempDir()
	clean(t, depCheck(t, app, Config{CacheDir: cache})) // the archive is cached
	if err := os.Remove(filepath.Join(app, sumFile)); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: cache, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	round, moved, _, err := fixHashes(t, s, false)
	if err != nil || round == nil || len(moved) != 1 {
		t.Fatalf("offline with the archive cached: %v %+v", err, round)
	}
	// No cache and no network: skipped, with a note; nothing written.
	app2 := urlApp(t, url, sha(arch))
	before := readFile(t, filepath.Join(app2, "pkg.mk"))
	s2, err := NewSession(Config{Root: app2, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	round, _, notes, err := fixHashes(t, s2, false)
	if err != nil || round != nil || len(notes) != 1 || !strings.Contains(notes[0], url) {
		t.Fatalf("no network: %v %+v %q", err, round, notes)
	}
	if readFile(t, filepath.Join(app2, "pkg.mk")) != before {
		t.Error("pkg.mk changed")
	}
}

// A Dep holding a comment keeps its hash and every comment.
func TestSum_FixKeepsCommentedDep(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	text := "const Package = pkg.Package(\n    name: \"app\",\n    deps: [\n        pkg.Dep(\n            name: \"ini\",\n            url: \"" + url +
		"\", // where it lives\n            hash: \"" + sha(arch) + "\", // its 1.2 hash\n        ),\n    ],\n)\n"
	app := writeTree(t, map[string]string{
		"pkg.mk":   text,
		"src/a.mk": "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n",
	})
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fixHashes(t, s, false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(app, "pkg.mk")); got != text {
		t.Errorf("pkg.mk changed:\n%s", got)
	}
}

// Inside a workspace member the round uses the workspace root's pkg.sum.
func TestSum_FixUsesWorkspaceSum(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	ws := writeTree(t, map[string]string{
		"pkg.mk":     "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"app\"],\n)\n",
		"app/pkg.mk": depManifest("app", urlDep("ini", url, sha(arch))),
		"app/a.mk":   "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n",
	})
	app := filepath.Join(ws, "app")
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	round, moved, _, err := fixHashes(t, s, false)
	if err != nil || round == nil || len(moved) != 1 {
		t.Fatalf("%v %+v", err, round)
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Error("the member got a pkg.sum of its own")
	}
	if got := readFile(t, filepath.Join(ws, sumFile)); got != moved[0].Line+"\n" {
		t.Errorf("workspace pkg.sum %q", got)
	}
}

// mk fix runs the round (the fix.go hook): the hash moves, and with no
// network the fix goes on to its other rounds with the round's note.
func TestSum_FixHookRunsRound(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := srv.URL + "/ini.tar.gz"
	app := urlApp(t, url, sha(arch))
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil || len(res.Rounds) == 0 || res.Rounds[0].Phase != "pkg-sum" {
		t.Fatalf("mk fix: %v %+v", err, res)
	}
	if strings.Contains(readFile(t, filepath.Join(app, "pkg.mk")), "hash:") {
		t.Error("the hash is still in pkg.mk")
	}
	app2 := urlApp(t, url, sha(arch))
	srv.Close()
	s2, err := NewSession(Config{Root: app2, StdDir: fakeStd(t), CacheDir: t.TempDir(), Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err = s2.Fix(FixOptions{DryRun: true})
	if err != nil || !strings.Contains(strings.Join(res.Final.Notes, "\n"), "keeps its 1.2 archive hash") {
		t.Fatalf("offline mk fix: %v %+v", err, res)
	}
}
