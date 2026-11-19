// Why: the server must know which package a file belongs to and check it with
// the open buffers in place of the files.
//
// What: root and the root registry, the check job, diagnostics mapping
// (mapReport), pull diagnostics and the dependants' rechecks.
//
// How: a root is the package whose pkg.mk holds the file. A check overlays the
// open buffers, keeps its session for exact answers and publishes, in path
// order, the earliest failing phase's diagnostics with related information,
// codeDescription and tags.

package lsp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// root is one workspace: a package directory, or a single file.
type root struct {
	key  string
	dir  string // the package directory, or the file's directory
	file string // the file in single-file mode; "" for a package
	// published are the files (by path key) whose last publish was not
	// empty, with their URIs.
	published map[string]string
	// fixes are the last check's diagnostics with fixes, by path key.
	fixes map[string][]fixedDiag
	// diags are the last check's diagnostics, by path key.
	diags map[string][]lspDiagnostic
	// checkedOnce: the root's first check (reported as progress) ran.
	checkedOnce bool
	// stdMods are std's importable modules, asked once (completion2.go).
	stdMods []string
	// genDir and genFiles are the root's generated files on disk (genview.go).
	genDir   string
	genFiles []string
	// kinds are the root's type kinds from `mk query kind`, valid while
	// its type declarations hash to kindsKey (semtok.go).
	kinds    map[string]string
	kindsKey string
	// depFiles are the dependencies' files on disk, and depDirs each
	// dependency's root module directory, by name (deps.go).
	depFiles []string
	depDirs  map[string]string
	// exact is the last check that reached the types phase, full the last
	// one that kept every package module's syntax (exact.go).
	exact, full *exact
	// triedFull: the root's first check, which keeps the syntax whatever
	// its outcome, has run (later checks do not; exact.go fullExact).
	triedFull bool
	// navSince: a request asked for a full session since the last check,
	// so the next check keeps the syntax and serves it.
	navSince bool
	// fullFailed are the root's texts, by path key, of the last on-demand
	// full check that did not reach types: it is not repeated over the
	// same texts.
	fullFailed map[string]string
	// checked: a check of the root published diagnostics; served are the
	// buffer texts it read, by path key (pull diagnostics).
	checked bool
	served  map[string]servedText
	// std: 1 for a std tree's root, checked as std (stdroot.go).
	std int8
}

// fixedDiag is a published diagnostic with its fixes as workspace edits.
type fixedDiag struct {
	diag  lspDiagnostic
	fixes []codeAction
	// verified: the compiles fixes were recompiled (codeActions).
	verified bool
}

// rootKeyOf finds the root of a file: the nearest directory upwards that
// holds pkg.mk, else the file alone; a std tree's files share its std root
// (stdroot.go). The key is "pkg:", "file:" or "std:" and the path key.
// The walk never reads rootCeiling or anything above it.
func rootKeyOf(path string) string {
	for dir := filepath.Dir(path); ; {
		if rootCeiling != "" && pathKey(dir) == rootCeiling {
			return "file:" + path
		}
		if std, manifest := stdTreeLevel(dir); std {
			return "std:" + dir
		} else if manifest {
			return "pkg:" + dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "file:" + path
		}
		dir = up
	}
}

// rootCeiling is the path key of a directory that rootKeyOf does not
// climb into, or "" for none. The tests set it to the system temporary
// directory: their fixtures live below it, and a stray pkg.mk left in it
// would otherwise make every fixture without a manifest part of one
// package spanning the whole directory.
var rootCeiling string

// foldKey is a root key's identity, case-folded where paths fold.
func foldKey(k string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(k)
	}
	return k
}

func (s *Server) rootFor(key string) *root {
	if r := s.roots[foldKey(key)]; r != nil {
		return r
	}
	r := &root{key: key, published: map[string]string{}, fixes: map[string][]fixedDiag{}}
	if p, ok := strings.CutPrefix(key, "pkg:"); ok {
		r.dir = p
	} else if p, ok := strings.CutPrefix(key, "std:"); ok {
		r.dir, r.std = p, 1
	} else {
		r.file = strings.TrimPrefix(key, "file:")
		r.dir = filepath.Dir(r.file)
	}
	s.roots[foldKey(key)] = r
	return r
}

// rootOfPath is the root of an open file's path.
func (s *Server) rootOfPath(path string) *root { return s.rootFor(rootKeyOf(path)) }

// overlay is Config.ReadFile for a check: an open buffer's text, else the
// disk's; served records what an open buffer gave, so a check that reaches
// the types phase can mark those texts good.
type overlay struct {
	s      *Server
	query  bool // answer from each buffer's last good text
	mu     sync.Mutex
	served map[string]servedText // by path key
}

// servedText is what an open buffer gave a check.
type servedText struct {
	path, raw string
	version   int
}

func (o *overlay) read(path string) ([]byte, error) {
	if o.query {
		if g, ok := o.s.docs.goodText(path); ok {
			return []byte(g), nil
		}
	}
	if d := o.s.docs.get(path); d != nil {
		o.mu.Lock()
		o.served[pathKey(d.path)] = servedText{path: d.path, raw: d.raw, version: d.version}
		o.mu.Unlock()
		return []byte(d.raw), nil
	}
	return os.ReadFile(path)
}

// bufferText is an open buffer's text, else the file's.
func (s *Server) bufferText(path string) ([]byte, error) {
	if d := s.docs.get(path); d != nil {
		return []byte(d.raw), nil
	}
	return os.ReadFile(path)
}

// session starts a driver session over the root: for a check over the
// buffers as they are, for a query over their last good texts.
func (s *Server) session(r *root) (*driver.Session, error) {
	sess, _, err := s.sessionWith(r, true, false)
	return sess, err
}

// sessionWith starts a session; keep sets Config.KeepSyntax, so every
// package module of the check keeps its syntax for TypeAt and UsesOf
// (exact.go). It costs every warm check its module-cache hits, so only a root's first check and an
// on-demand full check keep it.
func (s *Server) sessionWith(r *root, query, keep bool) (*driver.Session, *overlay, error) {
	cfg := driver.Config{
		Root:       r.dir,
		StdDir:     s.opt.StdDir,
		CacheDir:   s.opt.CacheDir,
		Jobs:       s.opt.Jobs,
		KeepSyntax: keep,
	}
	if r.file != "" {
		cfg.Root = r.file
	}
	if s.isStdRoot(r) {
		// std checked as std through browse.mk (stdroot.go), from source.
		p, err := s.browseFile(r)
		if err != nil {
			return nil, nil, err
		}
		cfg.Root, cfg.StdDir, cfg.CacheDir = p, stdCheckDir(r), s.stdCacheDir()
		cfg.NoCache, cfg.KeepSyntax, cfg.BrowseStd = true, true, true
	}
	o := &overlay{s: s, query: query, served: map[string]servedText{}}
	cfg.ReadFile = o.read
	if query {
		s.querySessions.Add(1)
	}
	if keep {
		s.fullSessions.Add(1)
	}
	sess, err := driver.NewSession(cfg)
	return sess, o, err
}

// absPath maps a SourceMap path to an OS path: package-relative paths
// under the root, single-file paths as given (absolute, since the session
// root is), std's under --std or the materialised copy (stdfiles.go). ""
// for a std file before std is on disk.
func (s *Server) absPath(r *root, p string) string {
	fp := filepath.FromSlash(p)
	if isGenPath(p) && r.genDir != "" {
		return filepath.Join(r.genDir, genRel(p))
	}
	if filepath.IsAbs(fp) {
		return filepath.Clean(fp)
	}
	if rest, ok := strings.CutPrefix(p, "std/"); ok && s.isStdRoot(r) {
		return filepath.Join(r.dir, filepath.FromSlash(rest))
	}
	if isFetchedPath(p) && !strings.HasPrefix(p, "std/") {
		if d := s.depsDirOf(r); d != "" {
			return filepath.Join(d, fp)
		}
	}
	if rest, ok := strings.CutPrefix(p, "std/"); ok {
		if s.stdDisk() == "" {
			return ""
		}
		return filepath.Join(s.stdDisk(), filepath.FromSlash(rest))
	}
	return filepath.Join(r.dir, fp)
}

// uriOf is the URI the client opened a path under, else the path's own.
func (s *Server) uriOf(path string) string {
	if d := s.docs.get(path); d != nil {
		return d.uri
	}
	return pathURI(path)
}

// checked is one check's outcome per file, by path key.
type checked struct {
	byFile map[string][]lspDiagnostic
	uris   map[string]string
	fixes  map[string][]fixedDiag
	served map[string]servedText
}

// checkRoot checks a root over the open buffers.
func (s *Server) checkRoot(r *root) checked {
	out := checked{byFile: map[string][]lspDiagnostic{}, uris: map[string]string{}, fixes: map[string][]fixedDiag{}}
	add := func(path string, d lspDiagnostic, fx []codeAction) {
		if s.isStdRoot(r) && !within(path, r.dir) {
			return // browse.mk's own (stdroot.go)
		}
		k := pathKey(path)
		out.byFile[k] = append(out.byFile[k], d)
		out.uris[k] = s.uriOf(path)
		if len(fx) > 0 {
			out.fixes[k] = append(out.fixes[k], fixedDiag{diag: d, fixes: fx})
		}
	}
	// The syntax is kept on the root's first check, and when a request
	// since the last check needed a full session: that request's next one
	// then reads this check's, and no edit pays two checks.
	keep := !r.triedFull || r.navSince
	r.triedFull, r.navSince = true, false
	sess, o, err := s.sessionWith(r, false, keep)
	if err != nil {
		// A usage error (no root, an unreadable file): one diagnostic at
		// the start of each open file of the root.
		for _, d := range s.docs.all() {
			if foldKey(rootKeyOf(d.path)) == foldKey(r.key) {
				add(d.path, lspDiagnostic{Severity: severityError, Source: "mk", Message: err.Error()}, nil)
			}
		}
		return out
	}
	res := sess.Check()
	s.checks.Add(1)
	s.materializeStd(r, res.Sources)
	s.materializeGen(r, res.Sources)
	s.materializeDeps(r, res.Sources)
	s.mapReport(r, res, add)
	if reachedTypes(res) {
		for _, st := range o.served {
			s.docs.setGood(st.path, st.raw)
		}
		s.keepExact(r, sess, res.Sources, keep)
	} else if keep {
		r.fullFailed = s.rootTexts(r) // fullExact does not repeat it
	}
	out.served = o.served
	return out
}

// runCheck checks a root and publishes its diagnostics: every file with
// some, every file that had some and has none now, and every open buffer
// of the root.
func (s *Server) runCheck(key string) {
	start := time.Now()
	r := s.rootFor(key)
	if !s.hasOpenDoc(key) {
		// Nothing of the root is shown any more (its last buffer closed):
		// clear what it published instead of checking it.
		for _, k := range sortedKeys(r.published) {
			s.publish(r.published[k], k, []lspDiagnostic{}, nil)
		}
		r.published, r.fixes, r.diags = map[string]string{}, nil, nil
		r.checked, r.served = false, nil
		return
	}
	done := s.progress(r)
	c := s.checkRoot(r)
	done()
	// In path order: map order never reaches the client.
	for _, k := range sortedKeys(c.byFile) {
		s.publish(c.uris[k], k, c.byFile[k], c.served)
		r.published[k] = c.uris[k]
	}
	cleared := map[string]bool{}
	for _, k := range sortedKeys(r.published) {
		if _, ok := c.byFile[k]; !ok {
			s.publish(r.published[k], k, []lspDiagnostic{}, c.served)
			delete(r.published, k)
			cleared[k] = true
		}
	}
	for _, d := range s.docs.all() {
		k := pathKey(d.path)
		if _, ok := c.byFile[k]; !ok && !cleared[k] && foldKey(rootKeyOf(d.path)) == foldKey(key) {
			s.publish(d.uri, k, []lspDiagnostic{}, c.served)
		}
	}
	r.fixes, r.diags = c.fixes, c.byFile
	r.checked, r.served = true, c.served
	s.logf("check %s: %d files with diagnostics, %v", key, len(c.byFile), time.Since(start))
}

// hasOpenDoc reports whether an open document belongs to the root key.
func (s *Server) hasOpenDoc(key string) bool {
	for _, d := range s.docs.all() {
		if foldKey(rootKeyOf(d.path)) == foldKey(key) {
			return true
		}
	}
	return false
}

// servedCurrent reports whether the root's last published check read
// every open buffer of the root as it is now.
func (s *Server) servedCurrent(r *root) bool {
	if !r.checked {
		return false
	}
	for _, d := range s.docs.all() {
		if foldKey(rootKeyOf(d.path)) != foldKey(r.key) {
			continue
		}
		st, ok := r.served[pathKey(d.path)]
		if !ok || st.version != d.version || st.raw != d.raw {
			return false
		}
	}
	return true
}

// checkDependants queues a check of each open root that depends on the
// root key through a path dependency, directly or through another open
// root (a dependency's buffer or file changed, and the dependants'
// checks read it). Each open root is queued at most once, so a cycle of
// path dependencies ends.
func (s *Server) checkDependants(key string) {
	dir, ok := strings.CutPrefix(key, "pkg:")
	if !ok {
		return // a single file is no one's dependency
	}
	open := map[string]string{} // path key of an open package root's directory to the directory
	for _, d := range s.docs.all() {
		if od, ok := strings.CutPrefix(rootKeyOf(d.path), "pkg:"); ok {
			open[pathKey(od)] = od
		}
	}
	reached := map[string]bool{pathKey(dir): true}
	for grew := true; grew; {
		grew = false
		for _, k := range sortedKeys(open) {
			od := open[k]
			if reached[k] {
				continue
			}
			for name, rel := range s.manifestDeps(&root{dir: od}) {
				if rel != "" && !vendors(od, name) && reached[pathKey(filepath.Join(od, filepath.FromSlash(rel)))] {
					reached[k], grew = true, true
					s.enqueue(job{check: "pkg:" + od})
					break
				}
			}
		}
	}
}

// pullDiagnostics answers textDocument/diagnostic with the document's
// diagnostics as a full report. They are the last published check's when
// it read the open buffers as they are; a check of the root queued since
// runs now in its place, and only otherwise does the pull check the root
// itself. So a client that pulls after each change pays one check per
// change, not two.
func (s *Server) pullDiagnostics(p documentParams) (any, *rpcError) {
	path, ok := uriPath(p.TextDocument.URI)
	if !ok {
		return nil, invalid(errors.New("not a file URI: " + p.TextDocument.URI))
	}
	r := s.rootOfPath(path)
	var byFile map[string][]lspDiagnostic
	switch {
	case !s.hasOpenDoc(r.key):
		c := s.checkRoot(r)
		r.fixes, r.diags = c.fixes, c.byFile
		byFile = c.byFile
	case s.takePending(r.key) || !s.servedCurrent(r):
		s.runCheck(r.key)
		byFile = r.diags
	default:
		byFile = r.diags
	}
	items := byFile[pathKey(path)]
	if items == nil {
		items = []lspDiagnostic{}
	}
	return map[string]any{"kind": "full", "items": items}, nil
}

// progress reports the first check of a root as work done progress, when
// the client accepts server-created tokens; it returns the end call.
func (s *Server) progress(r *root) func() {
	if !s.workDone.Load() || r.checkedOnce {
		r.checkedOnce = true
		return func() {}
	}
	r.checkedOnce = true
	s.progressN++
	token := "mk-check-" + itoa(s.progressN)
	_ = s.out.send(map[string]any{"jsonrpc": "2.0", "id": token, "method": "window/workDoneProgress/create", "params": map[string]any{"token": token}})
	_ = s.out.notify("$/progress", map[string]any{"token": token, "value": map[string]any{"kind": "begin", "title": "mk check", "message": filepath.Base(r.dir)}})
	return func() {
		_ = s.out.notify("$/progress", map[string]any{"token": token, "value": map[string]any{"kind": "end"}})
	}
}

// publish sends one file's diagnostics, with the version of the buffer
// text the check read, when it read one.
func (s *Server) publish(uri, key string, ds []lspDiagnostic, served map[string]servedText) {
	p := publishDiagnosticsParams{URI: uri, Diagnostics: ds}
	if st, ok := served[key]; ok {
		v := st.version
		p.Version = &v
	}
	_ = s.out.notify("textDocument/publishDiagnostics", p)
}

// mapReport turns a check's report into LSP diagnostics per file.
func (s *Server) mapReport(r *root, res driver.Result, add func(string, lspDiagnostic, []codeAction)) {
	u8 := s.utf8.Load()
	sm := res.Sources
	linesOf := map[source.FileID]lines{}
	where := func(sp source.Span) (string, Range, bool) {
		if sm == nil || sp.File == source.NoFile {
			return "", Range{}, false
		}
		f := sm.File(sp.File)
		path := s.absPath(r, f.Path)
		if path == "" {
			return "", Range{}, false
		}
		l, ok := linesOf[sp.File]
		if !ok {
			l = newLines(f.Text)
			linesOf[sp.File] = l
		}
		return path, l.span(int(sp.Start), int(sp.End), u8), true
	}
	for _, e := range res.Report.Diags {
		d := e.Diag
		ld := lspDiagnostic{Severity: severityError, Code: d.Code.String(), Source: "mk", Message: d.Message}
		if href := s.explainURI(r, ld.Code); href != "" {
			ld.CodeDescription = &codeDescription{Href: href}
		}
		if unnecessaryCodes[ld.Code] {
			ld.Tags = []int{tagUnnecessary}
		}
		path, rng, ok := where(d.At)
		if !ok {
			// No location the editor can show (E0000 without a span, a std
			// file): at the start of every open file of the root.
			if sm != nil && d.At.File != source.NoFile {
				ld.Message += " (at " + diag.Location(sm, d.At) + ")"
			}
			for _, doc := range s.docs.all() {
				if foldKey(rootKeyOf(doc.path)) == foldKey(r.key) {
					add(doc.path, ld, nil)
				}
			}
			continue
		}
		ld.Range = rng
		for _, f := range d.Facts {
			for _, at := range factSpans(f.Value) {
				if p, fr, ok := where(at); ok {
					ld.RelatedInformation = append(ld.RelatedInformation, relatedInformation{
						Location: Location{URI: s.uriOf(p), Range: fr}, Message: f.Key})
				}
			}
		}
		var actions []codeAction
		for _, fx := range d.Fixes {
			we := &workspaceEdit{Changes: map[string][]textEdit{}}
			okAll := true
			for _, ed := range fx.Edits {
				p, er, ok := where(ed.Span)
				if !ok {
					okAll = false
					break
				}
				u := s.uriOf(p)
				we.Changes[u] = append(we.Changes[u], textEdit{Range: er, NewText: ed.Text})
			}
			if !okAll || len(fx.Edits) == 0 {
				continue
			}
			actions = append(actions, codeAction{
				Title: fx.Class.String() + ": " + fx.Why, Kind: "quickfix",
				Diagnostics: []lspDiagnostic{ld}, IsPreferred: fx.Class == diag.Proven, Edit: we})
		}
		add(path, ld, actions)
	}
}

// reachedTypes reports whether a check got as far as the types phase (no
// parse, name or internal error), so its texts can answer queries.
func reachedTypes(res driver.Result) bool {
	for _, e := range res.Report.Diags {
		if p := e.Diag.Phase; p == diag.PhaseInternal || p < diag.PhaseTypes {
			return false
		}
	}
	return true
}

// factSpans are the locations a fact names.
func factSpans(f diag.Fact) []source.Span {
	switch f.Kind {
	case diag.FactAt, diag.FactLoan:
		if f.At.File != source.NoFile {
			return []source.Span{f.At}
		}
	case diag.FactList, diag.FactObject:
		var out []source.Span
		for _, x := range f.List {
			out = append(out, factSpans(x)...)
		}
		return out
	}
	return nil
}

// fileCache holds the scans of the root's files on disk, by path key, keyed
// again by size and modification time.
type fileCache struct {
	m map[string]*scanned
}

type scanned struct {
	size  int64
	mod   time.Time
	text  lines
	toks  []token
	decls []decl
}

func newFileCache() *fileCache { return &fileCache{m: map[string]*scanned{}} }

// srcFile is one file of a root as the editor sees it.
type srcFile struct {
	path  string
	text  lines
	toks  []token
	decls []decl
}

// fileOf is a file's current text and scans: the open buffer, else disk.
func (s *Server) fileOf(path string) (srcFile, bool) {
	if d := s.docs.get(path); d != nil {
		t, ds := d.scan()
		return srcFile{path: d.path, text: d.text, toks: t, decls: ds}, true
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return srcFile{}, false
	}
	k := pathKey(path)
	if c := s.files.m[k]; c != nil && c.size == fi.Size() && c.mod.Equal(fi.ModTime()) {
		return srcFile{path: path, text: c.text, toks: c.toks, decls: c.decls}, true
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return srcFile{}, false
	}
	l := newLines(source.Normalize(raw))
	toks := scan(l.s)
	c := &scanned{size: fi.Size(), mod: fi.ModTime(), text: l, toks: toks, decls: decls(l.s, toks)}
	s.files.m[k] = c
	return srcFile{path: path, text: l, toks: c.toks, decls: c.decls}, true
}

// rootFiles lists a root's .mk files, sorted: the file alone in single-file
// mode; else every .mk file under the package directory, skipping dot
// directories, gen/ views and nested packages.
func (s *Server) rootFiles(r *root) []string {
	if r.file != "" {
		return []string{r.file}
	}
	if s.isStdRoot(r) {
		return s.stdRootFiles(r)
	}
	seen := map[string]bool{}
	var out []string
	_ = filepath.WalkDir(r.dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			name := e.Name()
			if p != r.dir && (strings.HasPrefix(name, ".") || name == "gen") {
				return filepath.SkipDir
			}
			if p != r.dir {
				if _, err := os.Stat(filepath.Join(p, "pkg.mk")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(p, ".mk") && e.Name() != "pkg.mk" {
			seen[pathKey(p)] = true
			out = append(out, p)
		}
		return nil
	})
	// An open buffer not yet saved is part of its package too.
	for _, d := range s.docs.all() {
		if !seen[pathKey(d.path)] && foldKey(rootKeyOf(d.path)) == foldKey(r.key) {
			out = append(out, d.path)
		}
	}
	sort.Strings(out)
	return out
}

// joinModule is the directory of a package module's import path.
func joinModule(dir, mod string) string {
	return filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(mod, ".", "/")))
}

func dirOf(p string) string { return filepath.Dir(p) }
