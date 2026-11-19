// Why: source packages name the oldest version they accept, and the build
// must choose one version per identity the same way every time, with no
// solver and no lock file.
//
// What: selectVersions and selectVersionsWith (minimal version selection),
// requirement, selected, sourceReqs, mvsError, maxVersionsVisited and
// sortedIdentities.
//
// How: Go's algorithm, as a function over the manifests it reaches. Each
// requirement is a source and the oldest version accepted; every requirement
// reachable from the root's (and, in a workspace, every member's) is visited
// once, its version's manifest read and its own requirements visited; the
// build list holds, for each identity (a source and its major version), the
// highest version visited. test_deps count. The result depends only on the
// manifests reached, so it needs no lock file.
//
// Reading a manifest (from the cache, the seed or the network, with git's
// blob filter where the server offers it) is the caller's: selectVersions
// takes it as a function, so the walk is tested over an in-memory fake and
// the session gives it the fetching reader.

package driver

import (
	"fmt"
	"slices"
	"strings"
)

// requirement is one Dep with a source: the oldest version it accepts.
type requirement struct {
	Source  string
	Version semver
	// By is who states it: "" for a root (or member) manifest, else the
	// requiring version's "source@vX.Y.Z".
	By string
}

// identity is the requirement's identity, its source and major version.
func (r requirement) identity() string { return sourceMajor(r.Source, r.Version) }

// readManifest reads a source's manifest at a version. An error with an
// E0202 reason (a *fetch.Error, or a mvsError) stops the walk.
type readManifest func(source string, v semver) (*Manifest, error)

// mvsError is a walk fault with its E0202 reason.
type mvsError struct {
	Reason string
	Msg    string
}

func (e *mvsError) Error() string { return e.Msg }

// selected is one identity of the build list.
type selected struct {
	Source  string
	Version semver
	Man     *Manifest
	// Minimums are the versions each requirer states, by requirer ("" the
	// root), for mk tree and mk why.
	Minimums map[string]semver
}

// sourceReqs are the requirements a manifest states: its deps and
// test_deps that name a source, in that order.
func sourceReqs(m *Manifest, by string) ([]requirement, error) {
	var out []requirement
	for _, d := range append(slices.Clone(m.Deps), m.TestDeps...) {
		if d.Source == "" {
			continue
		}
		v, ok := parseSemver(d.Version)
		if !ok {
			return nil, &mvsError{"invalid", fmt.Sprintf("%s: version %q is not X.Y.Z", d.Field, d.Version)}
		}
		out = append(out, requirement{Source: d.Source, Version: v, By: by})
	}
	return out, nil
}

// maxVersionsVisited bounds the versions one walk reads: far above any real
// build, low enough that a server naming versions without end stops the
// walk.
const maxVersionsVisited = 10_000

// selectVersions runs minimal version selection from roots. It returns the
// build list by identity, and every version visited in visit order (each
// one's manifest was read, so each is a /pkg.mk line of pkg.sum).
func selectVersions(roots []requirement, read readManifest) (map[string]*selected, []requirement, error) {
	return selectVersionsWith(roots, read, sourceReqs)
}

// selectVersionsWith is selectVersions with the requirements of a visited
// version's manifest given by reqsOf: the build's walk applies the root's
// replace entries there, so a replaced source is never read.
func selectVersionsWith(roots []requirement, read readManifest, reqsOf func(*Manifest, string) ([]requirement, error)) (map[string]*selected, []requirement, error) {
	list := map[string]*selected{}
	visited := map[string]bool{} // source@version
	var order []requirement
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		id := r.identity()
		sel := list[id]
		if sel == nil {
			sel = &selected{Source: r.Source, Version: r.Version, Minimums: map[string]semver{}}
			list[id] = sel
		}
		if old, ok := sel.Minimums[r.By]; !ok || compareSemver(r.Version, old) > 0 {
			sel.Minimums[r.By] = r.Version
		}
		key := r.Source + "@" + r.Version.String()
		if visited[key] {
			continue
		}
		visited[key] = true
		if len(order) == maxVersionsVisited {
			// A hostile or broken server could name versions without end:
			// the walk stops, as the git client's limits do.
			return nil, nil, &mvsError{"fetch", fmt.Sprintf(
				"the version walk visited %d versions, its limit, at %s %s; a requirement graph this large is refused",
				maxVersionsVisited, r.Source, r.Version.Tag())}
		}
		order = append(order, r)
		m, err := read(r.Source, r.Version)
		if err != nil {
			return nil, nil, err
		}
		if compareSemver(r.Version, sel.Version) >= 0 {
			sel.Version, sel.Man = r.Version, m
		}
		reqs, err := reqsOf(m, key)
		if err != nil {
			return nil, nil, err
		}
		queue = append(queue, reqs...)
	}
	return list, order, nil
}

// sortedIdentities are the build list's identities in order.
func sortedIdentities(list map[string]*selected) []string {
	var out []string
	for id := range list {
		out = append(out, id)
	}
	slices.SortFunc(out, strings.Compare)
	return out
}
