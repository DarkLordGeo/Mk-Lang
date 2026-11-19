// Why: mk fix must apply only fixes it has proven, yet one fix often
// uncovers the next; the loop has to reach a fixed point across the module
// and report the whole chain.
//
// What: Session.Fix with FixOptions, FixRound, AppliedFix and FixResult;
// the progress tests better, passedManifest and clauseMovedUp; the
// candidate plumbing (fixCand, provenFixes, nonOverlapping, overlay); and
// the short-form round (shortForms, printShaped, shortFormEdit).
//
// How: candidates are applied to an in-memory overlay and the package is
// recompiled; a set is kept only when the recompile shows progress, so
// nothing unverified is written. A compiles or alternatives fix is offered,
// never applied.

package driver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/fmtspec"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// FixOptions are mk fix's.
type FixOptions struct {
	// DryRun computes the chain and writes nothing.
	DryRun bool
	// MaxRounds bounds the loop; 0 means 20.
	MaxRounds int
	// Canonical runs the canonical rounds of doc/style.md
	// (canonical.go) once the package checks clean.
	Canonical bool
}

// AppliedFix is one proven fix the loop kept.
type AppliedFix struct {
	Code diag.Code
	At   string // file:line:col of the diagnostic
	Why  string
}

// FixRound is one round of the chain: the phase it started from, how many
// diagnostics that phase had, and the fixes it applied.
type FixRound struct {
	Phase   string
	Diags   int
	Applied []AppliedFix
}

// FixResult is mk fix's outcome.
type FixResult struct {
	Rounds []FixRound
	// Final is the last check: what is left, with its offered fixes.
	Final Result
	// Files are the package files the chain changed, OS paths, sorted.
	Files []string
	// Stopped says why the loop ended: clean, no proven fix, no progress,
	// or the round bound.
	Stopped string
}

// Fix runs mk fix over the session's root. The session's own Config is
// used for every round, with an overlay of the edited files; files are
// written once at the end (with LF line ends) unless DryRun.
func (s *Session) Fix(opt FixOptions) (*FixResult, error) {
	maxRounds := opt.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 20
	}
	if s.cfg.StdTests != "" && s.cfg.StdDir == "" {
		// std is the subject, so its files are edited where MK_STD names
		// them; the embedded std cannot be.
		return nil, usagef("mk fix --std edits std's own files: set MK_STD to the std directory")
	}
	ov := &overlay{files: map[string]string{}, base: s.cfg.ReadFile}
	cfg := s.cfg
	cfg.ReadFile = ov.read
	cfg.GenView = false
	cfg.MaxDiags = diag.NoCap // progress counts every diagnostic, not the text cap's 50
	// The rounds read the package's syntax and typed bodies: the canonical
	// ones, and fs.Info's (fix_fsinfo.go), which every mk fix runs.
	cfg.KeepSyntax = true
	check := func() (Result, *Session, error) {
		ns, err := NewSession(cfg)
		if err != nil {
			return Result{}, nil, err
		}
		return ns.Check(), ns, nil
	}
	hashRound, hashFiles, hashNotes, err := s.fixArchiveHashes(opt.DryRun) // pkg.sum (fix_pkgsum.go)
	if err != nil {
		return nil, err
	}
	res, cur, err := check()
	if err != nil {
		return nil, err
	}
	out := &FixResult{}
	if hashRound != nil {
		out.Rounds = append(out.Rounds, *hashRound)
	}
	changed := map[string]bool{}
	for round := 0; ; round++ {
		if len(res.Report.Diags) == 0 {
			out.Stopped = "clean"
			break
		}
		if round == maxRounds {
			out.Stopped = "round bound"
			break
		}
		cands := s.provenFixes(res, ov)
		// Once std has removed fs.Info's 1.1 fields, their reads are
		// E0203; their rewrite is proven by this loop's recompile.
		for _, c := range cur.fsInfoFixes(res, ov) {
			if c.d != nil {
				cands = append(cands, c) // while the fields exist, the clean round below rewrites them
			}
		}
		cands = append(cands, s.becomeFixes(res, ov)...)      // fix_become.go
		cands = append(cands, cur.stackSizeFixes(res, ov)...) // fix_become.go
		cands = append(cands, s.hasFlowsFixes(res, ov)...)    // fix_hasflows.go
		if opt.Canonical {
			// The release round rewrites 1.0 code's E0701s
			// (canonical_release.go), proven by this loop's recompile.
			cands = append(cands, cur.releaseFixes(res, ov)...)
		}
		if len(cands) == 0 {
			out.Stopped = "no proven fix"
			break
		}
		fr := FixRound{Phase: res.Report.Phase.String(), Diags: len(res.Report.Diags)}
		// All non-overlapping fixes at once; when that is no better, each
		// alone, keeping the first that improves.
		// A release-round candidate (canonical_release.go) is tried
		// alone only, and kept only by releaseKept.
		var together []fixCand
		for _, c := range cands {
			if !releaseOnly([]fixCand{c}) {
				together = append(together, c)
			}
		}
		var tries [][]fixCand
		if len(together) > 0 {
			tries = append(tries, nonOverlapping(together))
		}
		if len(cands) > 1 || len(together) == 0 {
			for _, c := range cands {
				tries = append(tries, []fixCand{c})
			}
		}
		kept := false
		for _, set := range tries {
			saved := ov.snapshot()
			ov.apply(set)
			nres, ns, err := check()
			if err != nil {
				return nil, err
			}
			release := releaseOnly(set)
			if release && !s.releaseKept(set[0], nres, ov) {
				ov.restore(saved)
				continue
			}
			if better(nres.Report, res.Report) || clauseMovedUp(set, nres, res) || passedManifest(set, nres, res) {
				if release {
					fr.Phase = releasePhase
				}
				for _, c := range set {
					fr.Applied = append(fr.Applied, c.applied)
					changed[c.path] = true
				}
				res, cur = nres, ns
				kept = true
				break
			}
			ov.restore(saved)
		}
		if !kept {
			out.Stopped = "no progress"
			break
		}
		out.Rounds = append(out.Rounds, fr)
	}
	if out.Stopped == "clean" {
		// While std still has fs.Info's 1.1 fields beside the new:
		// the reads are rewritten, kept when the package still checks clean.
		if cands := cur.fsInfoFixes(res, ov); len(cands) > 0 {
			kept, nres, ns, err := applyProven(cands, ov, check)
			if err != nil {
				return nil, err
			}
			if len(kept) > 0 {
				fr := FixRound{Phase: fsInfoPhase}
				sortCands(kept)
				for _, c := range kept {
					fr.Applied = append(fr.Applied, c.applied)
					changed[c.path] = true
				}
				out.Rounds = append(out.Rounds, fr)
				res, cur = nres, ns
			}
		}
		if opt.Canonical {
			rounds, nres, ns, err := runCanonical(cur, res, ov, check, changed)
			if err != nil {
				return nil, err
			}
			out.Rounds = append(out.Rounds, rounds...)
			if len(rounds) > 0 {
				res, cur = nres, ns
			}
		}
		// A clean package's calls of a Print-shaped callee lose a
		// written () when the text has no hole, kept only when the result
		// still checks clean. All the edits are rechecked together and kept
		// or dropped together: each is proven by the same rule, so a failed
		// recheck means a rule bug, not one bad edit to single out.
		if cands := cur.shortForms(res, ov); len(cands) > 0 {
			saved := ov.snapshot()
			ov.apply(cands)
			nres, _, err := check()
			if err != nil {
				return nil, err
			}
			if len(nres.Report.Diags) == 0 {
				fr := FixRound{Phase: "short form"}
				for _, c := range cands {
					fr.Applied = append(fr.Applied, c.applied)
					changed[c.path] = true
				}
				out.Rounds = append(out.Rounds, fr)
				res = nres
			} else {
				ov.restore(saved)
			}
		}
	}
	// What is left is offered with its compiles fixes recompiled.
	cur.verifyCompiles(res, ov, cfg, &res)
	out.Final = res
	out.Final.Notes = append(out.Final.Notes, hashNotes...)
	for p := range changed {
		out.Files = append(out.Files, p)
	}
	sort.Strings(out.Files)
	if !opt.DryRun {
		for _, p := range out.Files {
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(p); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := os.WriteFile(p, []byte(ov.files[p]), mode); err != nil {
				return out, usagef("cannot write %s: %v", p, err)
			}
		}
	}
	out.Files = append(out.Files, hashFiles...) // written by the pkg-sum round itself
	sort.Strings(out.Files)
	return out, nil
}

// passedManifest is progress for a manifest fix: the manifest step failed
// before the set, and after it either passes, so the package's own stages
// run for the first time, or still fails but no longer at any diagnostic the
// set fixed and with no more diagnostics, as when a dependency's manifest is
// the next to need the same fix. The package's diagnostics are not
// comparable with the manifest's (the step reports alone), so a 1.1 manifest
// of a package with errors of its own is still fixed. An edit that makes the
// compiler fail is never progress.
func passedManifest(set []fixCand, after, before Result) bool {
	if before.Reached != StageManifest || internalCount(after.Report) > internalCount(before.Report) {
		return false
	}
	if after.Reached > StageManifest {
		return true
	}
	if len(after.Report.Diags) > len(before.Report.Diags) {
		return false
	}
	for _, e := range after.Report.Diags {
		sf := after.Sources.File(e.Diag.At.File)
		if sf == nil {
			continue
		}
		l, col := lineColOf(sf.Text, e.Diag.At.Start)
		at := sf.Path + ":" + itoaQ(l) + ":" + itoaQ(col)
		for _, c := range set {
			if c.applied.At == at {
				return false
			}
		}
	}
	return true
}

// clauseMovedUp is the progress rule for a flow clause: a set of one `into`
// or `from` clause fix (E0604, E0602) whose error moved to a caller, the
// next round's to fix. A clause is only ever added, so the loop cannot
// cycle; maxRounds bounds a long call chain.
func clauseMovedUp(set []fixCand, after, before Result) bool {
	if len(set) != 1 || set[0].d == nil {
		return false
	}
	if c := set[0].d.Code; c != diag.E0604 && c != diag.E0602 {
		return false
	}
	return internalCount(after.Report) == 0 && movedUp(after, before, set[0].d)
}

// better reports whether report a is progress over b: a later phase, or
// the same phase with fewer diagnostics.
func better(a, b diag.Report) bool {
	// An edit that makes the compiler fail is never progress, whatever the
	// other diagnostics do (PhaseInternal sorts first, but an E0000 beside
	// later-phase errors leaves Report.Phase at the later phase).
	if internalCount(a) > internalCount(b) {
		return false
	}
	if len(a.Diags) == 0 {
		return true
	}
	if a.Phase != b.Phase {
		return a.Phase > b.Phase
	}
	return len(a.Diags) < len(b.Diags)
}

func internalCount(r diag.Report) int {
	n := 0
	for _, e := range r.Diags {
		if e.Diag.Code == diag.E0000 {
			n++
		}
	}
	return n
}

// fixCand is one proven fix with its edits resolved to an OS path and
// byte offsets in the overlay's text.
type fixCand struct {
	path    string
	edits   []diag.Edit
	applied AppliedFix
	// d is the diagnostic the fix came from (nil for a rewrite round's).
	d *diag.Diagnostic
}

// provenFixes are the report's proven fixes whose edits all lie in one
// package file the session read (never std, never a generated file).
func (s *Session) provenFixes(res Result, ov *overlay) []fixCand {
	var out []fixCand
	for _, e := range res.Report.Diags {
		d := e.Diag
		for _, f := range d.Fixes {
			if f.Class != diag.Proven || len(f.Edits) == 0 { // never compiles or alternatives
				continue
			}
			path := ""
			ok := true
			for _, ed := range f.Edits {
				p := s.osPath(res.Sources, ed.Span.File, ov)
				if p == "" || (path != "" && p != path) {
					ok = false
					break
				}
				path = p
			}
			if !ok {
				continue
			}
			at := ""
			if sf := res.Sources.File(d.At.File); sf != nil {
				l, c := lineColOf(sf.Text, d.At.Start)
				at = sf.Path + ":" + itoaQ(l) + ":" + itoaQ(c)
			}
			out = append(out, fixCand{path: path, edits: f.Edits, applied: AppliedFix{Code: d.Code, At: at, Why: f.Why}, d: d})
		}
	}
	return out
}

func lineColOf(text string, off uint32) (int, int) {
	off = min(off, uint32(len(text)))
	before := text[:off]
	return strings.Count(before, "\n") + 1, int(off) - strings.LastIndexByte(before, '\n')
}

// osPath maps a source file of the report to the package file it was read
// from, or "" for std, a generated file or anything the overlay never read.
func (s *Session) osPath(sm *source.SourceMap, id source.FileID, ov *overlay) string {
	f := sm.File(id)
	if f == nil {
		return ""
	}
	if s.cfg.StdTests != "" {
		return s.stdOSPath(f.Path, ov)
	}
	rel, ok := strings.CutPrefix(f.Path, s.root.prefix)
	if !ok {
		return ""
	}
	p := filepath.Clean(filepath.Join(s.root.dir, filepath.FromSlash(rel)))
	if !ov.wasRead(p) {
		return ""
	}
	return p
}

// nonOverlapping keeps, in report order, the fixes whose edits overlap no
// edit kept before (the rest come back next round).
func nonOverlapping(cs []fixCand) []fixCand {
	var out []fixCand
	taken := map[string][][2]uint32{}
	for _, c := range cs {
		clash := false
		for _, e := range c.edits {
			for _, r := range taken[c.path] {
				if e.Span.Start < r[1] && r[0] < e.Span.End || e.Span.Start == r[0] {
					clash = true
				}
			}
		}
		if clash {
			continue
		}
		for _, e := range c.edits {
			taken[c.path] = append(taken[c.path], [2]uint32{e.Span.Start, e.Span.End})
		}
		out = append(out, c)
	}
	return out
}

// overlay is the fix loop's view of the package files: edited text by OS
// path, read through to the session's ReadFile otherwise. It keeps the
// normalized text a diagnostic's offsets index.
type overlay struct {
	mu    sync.Mutex
	files map[string]string // edited or read files, normalized
	seen  map[string]bool
	base  func(string) ([]byte, error)
}

func (o *overlay) read(p string) ([]byte, error) {
	p = filepath.Clean(p)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.seen == nil {
		o.seen = map[string]bool{}
	}
	if t, ok := o.files[p]; ok {
		o.seen[p] = true
		return []byte(t), nil
	}
	b, err := o.base(p)
	if err == nil {
		o.seen[p] = true
		o.files[p] = source.Normalize(b)
	}
	return b, err
}

func (o *overlay) wasRead(p string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.seen[p]
}

func (o *overlay) snapshot() map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := make(map[string]string, len(o.files))
	for k, v := range o.files {
		m[k] = v
	}
	return m
}

func (o *overlay) restore(m map[string]string) {
	o.mu.Lock()
	o.files = m
	o.mu.Unlock()
}

// apply makes every edit of the set, per file from the last offset back.
func (o *overlay) apply(set []fixCand) {
	o.mu.Lock()
	defer o.mu.Unlock()
	byFile := map[string][]diag.Edit{}
	var paths []string
	for _, c := range set {
		if _, ok := byFile[c.path]; !ok {
			paths = append(paths, c.path)
		}
		byFile[c.path] = append(byFile[c.path], c.edits...)
	}
	sort.Strings(paths)
	for _, p := range paths {
		text := o.files[p]
		es := byFile[p]
		sort.SliceStable(es, func(i, j int) bool { return es[i].Span.Start > es[j].Span.Start })
		for _, e := range es {
			if int(e.Span.End) > len(text) || e.Span.Start > e.Span.End {
				continue
			}
			text = text[:e.Span.Start] + e.Text + text[e.Span.End:]
		}
		o.files[p] = text
	}
}

// shortForms is the short form (no empty record) as mk fix writes it (no
// diagnostic, so its AppliedFix has no code): every call
// in a package body of a callee of the Print shape (its last two parameters
// a const str and a type parameter bounded by Fields[Format], after the
// receiver), of the runners lower expands Print and Arena.Fmt into, and of
// Writer.Str, the one call of a straight-line Print with no hole, whose
// last argument is a written () after a string literal with no hole
// loses ", ()". The callee's signature is known only here, after check;
// mk fmt, which sees tokens, rewrites only a bare Print. A call whose text
// holds a comment, or anything but one string literal before the (), is
// left as written.
func (s *Session) shortForms(res Result, ov *overlay) []fixCand {
	if s == nil || s.w == nil || s.w.sem5 == nil {
		return nil
	}
	k := s.w.sem5.known
	// An unpadded Print is lowered straight-line (internal/lower/format.go):
	// one with no hole and a non-empty text is one Writer.Str call at the
	// Print call's span. shortFormEdit judges the text, so a Writer.Str call
	// the user wrote, which never ends in a str literal and (), is left.
	str := types.NoItem
	if k.FmtRun != types.NoItem {
		key := s.w.items.Get(k.FmtRun).Key.String()
		if dot := strings.LastIndexByte(key, '.'); dot >= 0 {
			if id, ok := s.w.items.ByKey(types.MemberKey(key[:dot], "Writer", "Str")); ok {
				str = id
			}
		}
	}
	seen := map[source.Span]bool{}
	var out []fixCand
	mods := s.nonStdMods()
	if s.cfg.StdTests != "" {
		mods = s.subjectMods()
	}
	for _, m := range mods {
		if m.mod == nil {
			continue
		}
		for _, id := range m.mod.Items {
			switch s.w.items.Get(id).Kind {
			case types.KindFunc, types.KindLiteral, types.KindTest:
			default:
				continue
			}
			e := s.w.sem5.body(id)
			if e == nil || e.mir == nil {
				continue
			}
			b := e.mir
			for _, bl := range b.Blocks {
				t := bl.Term
				if t.Kind != mir.TermCall && t.Kind != mir.TermTailCall || t.Callee.Kind != mir.CallDirect || int(t.Span) >= len(b.Spans) {
					continue
				}
				fn := t.Callee.Fn.Item
				if fn != k.FmtRun && fn != k.FmtArena && (fn != str || str == types.NoItem) && !s.printShaped(fn, k.Format) {
					continue
				}
				sp := b.Spans[t.Span]
				if seen[sp] {
					continue
				}
				seen[sp] = true
				path := s.osPath(res.Sources, sp.File, ov)
				if path == "" {
					continue
				}
				ov.mu.Lock()
				text := ov.files[path]
				ov.mu.Unlock()
				ed, ok := shortFormEdit(sp, text)
				if !ok {
					continue
				}
				at := ""
				if sf := res.Sources.File(sp.File); sf != nil {
					l, c := lineColOf(sf.Text, sp.Start)
					at = sf.Path + ":" + itoaQ(l) + ":" + itoaQ(c)
				}
				out = append(out, fixCand{path: path, edits: []diag.Edit{ed},
					applied: AppliedFix{At: at, Why: "the text has no hole: the record () is left out"}})
			}
		}
	}
	// In source order, file by file, whatever order the bodies came in.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].edits[0].Span.Start < out[j].edits[0].Span.Start
	})
	return out
}

// printShaped reports whether fn's signature is of the Print shape
// (check's formatShaped).
func (s *Session) printShaped(fn types.ItemID, format types.ItemID) bool {
	if fn == types.NoItem || format == types.NoItem {
		return false
	}
	fd, ok := s.w.items.Get(fn).Decl.(*types.FuncDecl)
	if !ok {
		return false
	}
	ps := fd.Sig.Params
	if fd.Sig.Receiver != types.NoReceiver && len(ps) > 0 {
		ps = ps[1:]
	}
	if len(ps) < 2 {
		return false
	}
	f, a := ps[len(ps)-2], ps[len(ps)-1]
	ty := s.w.tab.Get(a.Type)
	if !f.Const || f.Type != types.Str || a.Const || ty.Kind != types.KParam || int(ty.Index) >= len(fd.Sig.Generics) {
		return false
	}
	for _, b := range fd.Sig.Generics[ty.Index].Bounds {
		if b.Kind == types.BoundFields && b.Inner == types.BoundInterface && b.Item == format {
			return true
		}
	}
	return false
}

// shortFormEdit is the edit that removes ", ()" from the call at sp in
// text: its argument list is the last bracket group, its last argument is
// () and the one before it a single string literal whose text has no hole
// and no scan problem. False for anything else, and for a call that holds a
// comment.
func shortFormEdit(sp source.Span, text string) (diag.Edit, bool) {
	if sp.End > uint32(len(text)) || sp.Start >= sp.End {
		return diag.Edit{}, false
	}
	toks, ds := syntax.Lex(sp.File, text[sp.Start:sp.End])
	if len(ds) > 0 {
		return diag.Edit{}, false
	}
	var ts []ast.Token
	for _, t := range toks {
		switch t.Kind {
		case ast.TokWhitespace, ast.TokNewline, ast.TokTerm, ast.TokEOF:
			continue
		case ast.TokComment, ast.TokDocComment:
			return diag.Edit{}, false
		}
		ts = append(ts, t)
	}
	n := len(ts)
	// ... , "text" , ( ) )
	if n < 6 || ts[n-1].Kind != ast.TokRParen || ts[n-2].Kind != ast.TokRParen || ts[n-3].Kind != ast.TokLParen ||
		ts[n-4].Kind != ast.TokComma || (ts[n-6].Kind != ast.TokComma && ts[n-6].Kind != ast.TokLParen) {
		return diag.Edit{}, false
	}
	// Between the comma or ( at n-6 and the last ) every bracket is
	// balanced, so the record is the last argument of the call's own list.
	lit := ts[n-5]
	src := text[sp.Start+lit.Start : sp.Start+lit.End()]
	switch lit.Kind {
	case ast.TokStr, ast.TokRawStr, ast.TokMultiStr:
	default:
		return diag.Edit{}, false
	}
	val, ok := syntax.StringValue(lit.Kind, src)
	if !ok {
		return diag.Edit{}, false
	}
	if holes, _, problem := fmtspec.Scan(val); len(holes) > 0 || problem != "" {
		return diag.Edit{}, false
	}
	return diag.Edit{Span: source.Span{File: sp.File, Start: sp.Start + lit.End(), End: sp.Start + ts[n-1].Start}}, true
}
