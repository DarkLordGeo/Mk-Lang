// Why: a url Dep pinned by its archive hash (`hash: "sha256:..."`, the 1.2
// form) should move to a pkg.sum tree line, and `mk fix` can do that
// without touching anything else in pkg.mk.
//
// What: fixArchiveHashes and archiveHashes (the round), archiveHashFix,
// fixFetcher, sumDir and holdsComment.
//
// How: for each such Dep of the package's own pkg.mk (deps and test_deps),
// the archive is fetched (or read from the cache) and checked against the
// Dep's hash, unpacked, and its tree hash written as pkg.sum's `<url> -
// - tree-sha256:<hex>` line; then the `hash` argument leaves the Dep, by a
// span splice that keeps every other byte of pkg.mk. pkg.sum is written
// first, then pkg.mk, each atomically, so an interrupted fix leaves a
// manifest that still builds. Fix (fix.go) calls fixArchiveHashes before its
// rounds.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mk/internal/ast"
	"mk/internal/cache"
	"mk/internal/diag"
	"mk/internal/fetch"
	"mk/internal/source"
	"mk/internal/syntax"
)

// archiveHashFix is one Dep the round moves: its manifest position and
// the pkg.sum line that replaces its hash.
type archiveHashFix struct {
	At   string // pkg.mk:line:col of the Dep
	URL  string
	Line string // the pkg.sum line written
}

// fixArchiveHashes is the pkg-sum round over the package's pkg.mk, as mk fix
// runs it (the hook in fix.go): the round (Phase "pkg-sum", one Applied
// per Dep moved; nil when there is none), the files it writes (pkg.mk and
// pkg.sum; written already unless dryRun), the notes for mk fix to print,
// and an error for an I/O failure, which leaves both files as they were.
func (s *Session) fixArchiveHashes(dryRun bool) (*FixRound, []string, []string, error) {
	round, _, notes, err := s.archiveHashes(dryRun)
	if round == nil || err != nil {
		return round, nil, notes, err
	}
	return round, []string{filepath.Join(s.root.dir, "pkg.mk"), filepath.Join(s.sumDir(), sumFile)}, notes, nil
}

// archiveHashes is fixArchiveHashes with the moves made. The archive is
// read through the build's cache, as the build reads it (fetcherFor), so
// an offline fix works where the offline build does; a Dep whose archive
// cannot be fetched keeps its hash, with a note, and the round goes on.
// A Dep holding a comment keeps its hash too, with a note: a
// splice would lose or misplace the comment. The pkg.sum is the
// workspace root's when the package is a member.
func (s *Session) archiveHashes(dryRun bool) (*FixRound, []archiveHashFix, []string, error) {
	if s.root.kind != rootPackage {
		return nil, nil, nil, nil
	}
	mp := filepath.Join(s.root.dir, "pkg.mk")
	raw, err := os.ReadFile(mp)
	if err != nil {
		return nil, nil, nil, err
	}
	sm := source.NewSourceMap()
	f := sm.Add("pkg.mk", raw)
	in := source.NewInterner()
	file, ds := syntax.ParseFile(f, in, syntax.Options{})
	if len(ds) > 0 || file == nil {
		return nil, nil, nil, nil // mk fix's own rounds report the parse error
	}
	text := f.Text
	var edits []diag.Edit
	var moved []archiveHashFix
	// pkg.sum is read at the first Dep with a 1.2 hash, never before: a
	// package without one is not the round's, whatever file named pkg.sum
	// sits beside it. A read failure is a note.
	var sumDir string
	var sum *pkgSum
	stop := false
	var fetcher *fetch.Fetcher
	var notes []string
	ast.Inspect(file, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return n != nil
		}
		if !isPkgCall(c, "Dep", in) {
			return true
		}
		args := map[string]string{}
		hashAt := -1
		for i, a := range c.Args {
			if a == nil || a.Name == nil {
				return true
			}
			lit, ok := a.Value.(*ast.BasicLit)
			if !ok || lit.Kind != ast.TokStr {
				continue
			}
			v, ok := syntax.StringValue(lit.Kind, lit.Raw) // Mk's string rules
			if !ok {
				continue
			}
			name := in.Str(a.Name.Name)
			args[name] = string(v)
			if name == "hash" {
				hashAt = i
			}
		}
		if args["url"] == "" || args["hash"] == "" || hashAt < 0 || checkSumURL(args["url"]) != nil {
			return true
		}
		h, ok := fetch.ParseHash(args["hash"])
		if !ok {
			return true
		}
		line, col := lineColOf(text, c.Span().Start)
		at := "pkg.mk:" + itoaQ(line) + ":" + itoaQ(col)
		if stop {
			return true
		}
		if s.cfg.Locked {
			// --locked writes no pkg.sum: the hash stays.
			notes = append(notes, fmt.Sprintf("note: %s: dependency %s keeps its 1.2 archive hash: mk fix --locked writes no pkg.sum", at, args["url"]))
			return true
		}
		if sum == nil {
			sumDir = s.sumDir()
			var err error
			if sum, err = readPkgSum(sumDir); err != nil {
				notes = append(notes, fmt.Sprintf("note: %s does not read (%v), so mk fix leaves every 1.2 archive hash in pkg.mk", filepath.Join(sumDir, sumFile), err))
				stop = true
				return true
			}
			fetcher = s.fixFetcher()
		}
		if holdsComment(text, c) {
			notes = append(notes, fmt.Sprintf("note: %s: dependency %s keeps its 1.2 archive hash: the Dep holds a comment, which mk fix would lose; remove hash: by hand and build, and the build records the tree in pkg.sum", at, args["url"]))
			return true
		}
		data, _, err := fetcher.Archive(args["url"], h)
		var tr *fetch.Tree
		if err == nil {
			tr, err = fetch.Unpack(data, fetch.Limits{})
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("note: %s: dependency %s keeps its 1.2 archive hash, which the build still checks: %v", at, args["url"], err))
			return true
		}
		l := sumLine{Source: args["url"], Hash: tr.Hash().String()}
		if reason, old := sum.check(l); reason != "" {
			notes = append(notes, fmt.Sprintf("note: %s: dependency %s keeps its 1.2 archive hash: pkg.sum records %s, but its tree has %s (%s)", at, l.Source, old.Hash, l.Hash, reason))
			return true
		}
		sum.add(l)
		moved = append(moved, archiveHashFix{At: at, URL: l.Source, Line: l.String()})
		edits = append(edits, argRemovals(text, c, []int{hashAt})...)
		return true
	})
	if len(moved) == 0 {
		return nil, nil, notes, nil
	}
	round := &FixRound{Phase: "pkg-sum"}
	for _, m := range moved {
		round.Applied = append(round.Applied, AppliedFix{At: m.At,
			Why: "1.2's archive hash moves into pkg.sum as the tree's hash, " + m.Line})
	}
	if dryRun {
		return round, moved, notes, nil
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		text = text[:e.Span.Start] + e.Text + text[e.Span.End:]
	}
	if err := writePkgSum(sumDir, sum); err != nil {
		return nil, nil, nil, err
	}
	if err := writeFileAtomic(mp, []byte(text)); err != nil {
		return nil, nil, nil, err
	}
	return round, moved, notes, nil
}

// fixFetcher is the round's fetcher, made as the build's is (fetcherFor):
// the content cache at the session's cache directory, none under
// --no-cache.
func (s *Session) fixFetcher() *fetch.Fetcher {
	f := &fetch.Fetcher{Client: s.cfg.TestHooks.HTTPClient, Offline: s.cfg.Offline}
	if !s.cfg.NoCache {
		if st, err := cache.Open(s.cacheDir(), cache.Options{Budget: s.cfg.CacheBudget, Now: s.cfg.TestHooks.CacheNow}); err == nil {
			f.Store = st
		}
	}
	return f
}

// sumDir is the directory whose pkg.sum the package uses: its workspace
// root's when it is a member, else its own. A manifest that does not load
// leaves the package's own; mk fix's rounds report the fault.
func (s *Session) sumDir() string {
	cfg := s.cfg
	cfg.CacheDir = "" // only the manifest step runs, which keeps nothing
	ns, err := NewSession(cfg)
	if err != nil {
		return s.root.dir
	}
	if ws, ds := ns.workspaceOnly(); len(ds) == 0 && ws != nil {
		return ws.dir
	}
	return s.root.dir
}

// holdsComment reports whether the call c holds a comment: a "//" outside
// its arguments' string literals (a url holds "//").
func holdsComment(text string, c *ast.CallExpr) bool {
	start := int(c.Span().Start)
	b := []byte(text[start:c.Span().End])
	for _, a := range c.Args {
		if a == nil {
			continue
		}
		if lit, ok := a.Value.(*ast.BasicLit); ok && lit.Kind == ast.TokStr {
			for i := int(lit.Span().Start) - start; i < int(lit.Span().End)-start; i++ {
				b[i] = ' '
			}
		}
	}
	return strings.Contains(string(b), "//")
}
