// Why: minimal version selection must pick the same versions every time,
// read each manifest once, and end on cycles and endless servers.
// What: TestSum_MVSHighestVisited and its neighbours over fakeVersions.
// How: an in-memory set of manifests stands for the servers, so nothing is
// fetched; the tests check the chosen versions and the manifests read.

package driver

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeVersions maps "source@version" to the requirements that version's
// manifest states (as "source@version" strings).
type fakeVersions map[string][]string

func (f fakeVersions) read(reads *[]string) readManifest {
	return func(source string, v semver) (*Manifest, error) {
		key := source + "@" + v.String()
		reqs, ok := f[key]
		if !ok {
			return nil, &mvsError{"no-version", "no tag v" + v.String() + " in " + source}
		}
		*reads = append(*reads, key)
		m := &Manifest{}
		for i, r := range reqs {
			var s, ver string
			for j := len(r) - 1; j >= 0; j-- {
				if r[j] == '@' {
					s, ver = r[:j], r[j+1:]
					break
				}
			}
			m.Deps = append(m.Deps, Dep{Name: fmt.Sprintf("d%d", i), Source: s, Version: ver, Field: fmt.Sprintf("deps[%d]", i)})
		}
		return m, nil
	}
}

func mustSemver(t *testing.T, s string) semver {
	v, ok := parseSemver(s)
	if !ok {
		t.Fatalf("version %q", s)
	}
	return v
}

func TestSum_MVSHighestVisited(t *testing.T) {
	// Go's example shape: A needs B 1.2 and C 1.2; B 1.2 needs D 1.3; C 1.2
	// needs D 1.4; D 1.4 needs E 1.1 (and E 1.2 is never named). D 2.x is
	// another identity.
	f := fakeVersions{
		"x.io/b@1.2.0": {"x.io/d@1.3.0"},
		"x.io/c@1.2.0": {"x.io/d@1.4.0", "x.io/d@2.0.0"},
		"x.io/d@1.3.0": {"x.io/e@1.0.0"},
		"x.io/d@1.4.0": {"x.io/e@1.1.0"},
		"x.io/d@2.0.0": {},
		"x.io/e@1.0.0": {},
		"x.io/e@1.1.0": {},
		"x.io/e@1.2.0": {},
	}
	var reads []string
	roots := []requirement{{Source: "x.io/b", Version: mustSemver(t, "1.2.0")}, {Source: "x.io/c", Version: mustSemver(t, "1.2.0")}}
	list, visited, err := selectVersions(roots, f.read(&reads))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"x.io/b@v1": "1.2.0", "x.io/c@v1": "1.2.0", "x.io/d@v1": "1.4.0", "x.io/d@v2": "2.0.0", "x.io/e@v1": "1.1.0"}
	if len(list) != len(want) {
		t.Errorf("build list %v", sortedIdentities(list))
	}
	for id, v := range want {
		if sel := list[id]; sel == nil || sel.Version.String() != v || sel.Man == nil {
			t.Errorf("%s: %+v, want %s", id, sel, v)
		}
	}
	// Every reachable version's manifest is read once; e 1.2.0 never.
	if len(visited) != 7 || len(reads) != 7 {
		t.Errorf("visited %d, read %v", len(visited), reads)
	}
	if m := list["x.io/d@v1"].Minimums; m["x.io/b@1.2.0"].String() != "1.3.0" || m["x.io/c@1.2.0"].String() != "1.4.0" {
		t.Errorf("minimums %v", m)
	}
}

func TestSum_MVSNoVersion(t *testing.T) {
	var reads []string
	_, _, err := selectVersions([]requirement{{Source: "x.io/b", Version: mustSemver(t, "9.0.0")}}, fakeVersions{}.read(&reads))
	var me *mvsError
	if !errors.As(err, &me) || me.Reason != "no-version" {
		t.Errorf("err %v", err)
	}
}

func TestSum_MVSCycleTerminates(t *testing.T) {
	f := fakeVersions{"x.io/a@1.0.0": {"x.io/b@1.0.0"}, "x.io/b@1.0.0": {"x.io/a@1.0.0"}}
	var reads []string
	list, _, err := selectVersions([]requirement{{Source: "x.io/a", Version: mustSemver(t, "1.0.0")}}, f.read(&reads))
	if err != nil || len(list) != 2 || len(reads) != 2 {
		t.Errorf("%v %v %v", err, list, reads)
	}
}

// A server that names versions without end stops the walk with E0202
// fetch, naming the limit.
func TestSum_MVSVisitLimit(t *testing.T) {
	n := 0
	read := func(source string, v semver) (*Manifest, error) {
		n++
		next := semver{Major: 1, Minor: v.Minor + 1}
		return &Manifest{Deps: []Dep{{Name: "x", Source: source, Version: next.String(), Field: "deps[0]"}}}, nil
	}
	_, _, err := selectVersions([]requirement{{Source: "x.io/a", Version: mustSemver(t, "1.0.0")}}, read)
	var me *mvsError
	if !errors.As(err, &me) || me.Reason != "fetch" || !strings.Contains(me.Msg, "10000 versions") || n != maxVersionsVisited {
		t.Errorf("err %v after %d reads", err, n)
	}
}
