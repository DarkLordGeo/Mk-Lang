// Why: under --no-cache a fetched package has no tree in the package
// cache, so its C sources need a directory of their own that two versions
// cannot share by accident.
// What: TestCache_CDepsKeyedByTree.
// How: the C sources are unpacked under cdeps/, in a directory named by
// the identity and the tree's hash, since one identity (a source and its
// major) holds every minor version.

package driver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCache_CDepsKeyedByTree(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	gs.tag("someone/toml", "1.1.0", tomlFiles("11"))
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{"app/a.mk": "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n"})
	cache := t.TempDir()
	var dirs []string
	for _, v := range []string{"1.0.0", "1.1.0"} {
		writeFiles(t, ws, map[string]string{"app/pkg.mk": depManifest("app", srcDep("toml", src, v))})
		cfg := sourceCfg(gs, cache)
		cfg.Root, cfg.StdDir, cfg.NoCache = filepath.Join(ws, "app"), fakeStd(t), true
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		clean(t, s.Check())
		if len(s.deps) != 1 || s.deps[0].osDir != "" || s.deps[0].version != v {
			t.Fatalf("%s: deps %+v; want one in memory", v, s.deps)
		}
		dir, d := s.depCDir(s.deps[0])
		if d != nil {
			t.Fatal(d.Message)
		}
		b, err := os.ReadFile(filepath.Join(dir, "src", "toml.mk"))
		if want := tomlFiles(map[string]string{"1.0.0": "10", "1.1.0": "11"}[v])["src/toml.mk"]; err != nil || string(b) != want {
			t.Errorf("%s: %s holds %q, %v; want %q", v, dir, b, err, want)
		}
		dirs = append(dirs, dir)
	}
	if dirs[0] == dirs[1] {
		t.Errorf("two versions of one major share %s", dirs[0])
	}
}
