// Why: `mk repl` lets a programmer try expressions and declarations against
// a package without writing a file, and it must give the same answers as a
// real build: same checker, same evaluator, same diagnostics.
//
// What: Options and Session (New, Start, Run, Serve, ServeUntil, Entry,
// Close), and the entry handlers doExpr, doDecl, doImport, doQuery and the
// ':' commands.
//
// How: every entry is checked by a fresh driver.Session over a scratch file
// that holds the session's imports and declarations. An expression becomes
// a const, evaluated during the check under the usual fuel rule, and its
// value and type come back through the `sig` query.

package repl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
	"mk/internal/types"
)

// Options configure a session.
type Options struct {
	// Root is the package directory (with pkg.mk), a directory of .mk files
	// (directory mode: one module), or "" for the working directory when it
	// holds pkg.mk, else a standalone session over std alone. A .mk file is
	// refused: single-file mode forms one module from that file only.
	Root string
	// Config is the base driver configuration (target, std, jobs, cache);
	// Root and GenView are set by the session. An empty CacheDir with
	// MK_CACHE unset means <root>/.mk-cache in package and directory mode
	// (shared with mk check) and the user cache directory's mk/repl when
	// standalone.
	Config driver.Config
	// Fuel is the step limit written on each evaluation (`fuel N`); 0 leaves
	// the language default, types.DefaultFuel.
	Fuel uint64
	// Prompt writes "mk> " and "... " before each line read.
	Prompt bool
}

// Session is one REPL session: its accepted imports and declarations, and
// the scratch file they are checked in. Not safe for concurrent use.
type Session struct {
	opt  Options
	cfg  driver.Config
	file string // absolute path of the scratch file
	base string // its base name
	tmp  string // standalone: the temporary directory to remove
	mode string // "package", "directory" or "standalone"

	imports []imp
	decls   []decl
	n       int    // entries numbered so far
	fuel    uint64 // 0: the language default

	// mu orders scratch writes against Close, which a signal handler may
	// call while an entry runs: after Close nothing writes the file again.
	mu     sync.Mutex
	closed bool

	// mainStub: the scratch file declares an empty Main (Start found the
	// root module a bin root with none).
	mainStub bool
}

// imp is an accepted import.
type imp struct {
	n    int    // the entry that made it
	name string // the name code uses: the alias or the path's last segment
	line string
}

// decl is an accepted declaration.
type decl struct {
	n    int
	name string // "" when no later entry replaces it by name
	text string // as typed (:decls)
	// frozen is what the scratch file holds instead of text, or "": a kept
	// const with a scalar or str value is written with that value, `const
	// Big: int = 5000000`, so later entries do not evaluate it again. It
	// keeps the value it had when it was entered.
	frozen string
}

// scratch is the declaration's text in the scratch file.
func (d *decl) scratch() string {
	if d.frozen != "" {
		return d.frozen
	}
	return d.text
}

// scratchHeader is the scratch file's first line.
const scratchHeader = "// mk repl scratch file, removed when its session ends; not part of the package"

// New makes a session and its scratch file. The error is a usage error
// (exit status 2 in the command).
func New(opt Options) (*Session, error) {
	s := &Session{opt: opt, cfg: opt.Config, fuel: opt.Fuel}
	s.cfg.GenView = false // the REPL writes nothing into the tree but its scratch file
	root := opt.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot use the working directory: %v", err)
		}
		if isFile(filepath.Join(wd, "pkg.mk")) {
			root = wd
		}
	}
	if root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("cannot use root %s: %v", root, err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("cannot read root %s: %v", root, err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("root %s is not a directory: mk repl takes a package or a directory of .mk files", root)
		}
		s.mode = "directory"
		if isFile(filepath.Join(abs, "pkg.mk")) {
			s.mode = "package"
		}
		s.base = ".mk-repl-" + strconv.Itoa(os.Getpid()) + ".mk"
		s.file = filepath.Join(abs, s.base)
		if _, err := os.Lstat(s.file); err == nil {
			return nil, fmt.Errorf("%s exists: another mk repl may be running in this package (remove the file if not)", s.file)
		}
		if other := otherScratch(abs, s.base); other != "" {
			// Every check of the package, this session's included, reads
			// another session's scratch file: a live session's names would
			// collide with this one's, and a dead one's (killed with no
			// chance to clean up) may not even check.
			return nil, fmt.Errorf("%s is another mk repl session's scratch file: one runs in this package, or one ended without removing it, and every check of the package reads it (remove it if no mk repl runs here)", other)
		}
		s.cfg.Root = abs
	} else {
		s.mode = "standalone"
		dir, err := os.MkdirTemp("", "mk-repl-")
		if err != nil {
			return nil, fmt.Errorf("cannot make the scratch directory: %v", err)
		}
		s.tmp = dir
		s.base = "repl.mk"
		s.file = filepath.Join(dir, s.base)
		s.cfg.Root = s.file
		if s.cfg.CacheDir == "" && os.Getenv("MK_CACHE") == "" {
			if uc, err := os.UserCacheDir(); err == nil {
				s.cfg.CacheDir = filepath.Join(uc, "mk", "repl")
			}
		}
	}
	if err := s.write(scratchHeader + "\n"); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// otherScratch is the path of another session's scratch file in dir (named
// .mk-repl-*.mk and starting with scratchHeader), or "".
func otherScratch(dir, own string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		n := e.Name()
		if n == own || !strings.HasPrefix(n, ".mk-repl-") || !strings.HasSuffix(n, ".mk") || !e.Type().IsRegular() {
			continue
		}
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		head := make([]byte, len(scratchHeader))
		k, _ := io.ReadFull(f, head)
		f.Close()
		if string(head[:k]) == scratchHeader {
			return filepath.Join(dir, n)
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// Close removes the scratch file (and the standalone directory).
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	var err error
	if s.file != "" {
		if e := os.Remove(s.file); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = e
		}
	}
	if s.tmp != "" {
		if e := os.RemoveAll(s.tmp); e != nil {
			err = e
		}
	}
	s.file, s.tmp = "", ""
	return err
}

// errClosed is the error of a scratch write after Close.
var errClosed = errors.New("the session is closed")

// write replaces the scratch file's text.
func (s *Session) write(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errClosed
	}
	if err := os.WriteFile(s.file, []byte(text), 0o644); err != nil {
		return fmt.Errorf("cannot write the scratch file: %v", err)
	}
	return nil
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ScratchFile is the absolute path of the scratch file (tests read it).
func (s *Session) ScratchFile() string { return s.file }

// Run reads entries from in until its end or :quit, writing values to out
// and diagnostics to errw. It returns the command's exit status: 0, or 2
// when the session cannot start.
func Run(in io.Reader, out, errw io.Writer, opt Options) int {
	s, err := New(opt)
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return 2
	}
	defer s.Close()
	return s.Serve(in, out, errw)
}

// ServeUntil is Serve on a goroutine that returns early when stop receives
// (Ctrl+C, a termination or a hang-up): the session is closed first, so its
// scratch file is gone and no entry writes it again, and the status is 128
// plus the signal's number (130 for Ctrl+C). A read of the console cannot
// be interrupted portably, so the reading goroutine is left to the process's
// exit. A panic in an entry is an internal error, status 3.
func (s *Session) ServeUntil(in io.Reader, out, errw io.Writer, stop <-chan os.Signal) int {
	done := make(chan int, 1)
	go func() {
		if ie := source.Guard("mk repl", source.Span{}, func() { done <- s.Serve(in, out, errw) }); ie != nil {
			fmt.Fprintf(errw, "mk repl: %s\n%s", ie.String(), ie.Stack)
			done <- 3
		}
	}()
	select {
	case code := <-done:
		return code
	case sg := <-stop:
		s.Close()
		fmt.Fprintln(errw)
		if n, ok := sg.(syscall.Signal); ok && n > 0 {
			return 128 + int(n)
		}
		return 130
	}
}

// Serve is Run over a session already made: Start, then the entries of in.
// It returns 0 at the end of input or :quit, and 1 when Close ended the
// session while it read.
func (s *Session) Serve(in io.Reader, out, errw io.Writer) int {
	opt := s.opt
	s.Start(errw)
	r := newReader(bufio.NewScanner(in))
	prompt := func(cont bool) {
		if !opt.Prompt {
			return
		}
		if cont {
			io.WriteString(out, "... ")
		} else {
			io.WriteString(out, "mk> ")
		}
	}
	for {
		e, ok := r.next(prompt)
		if s.isClosed() {
			return 1
		}
		if !ok {
			if opt.Prompt {
				io.WriteString(out, "\n")
			}
			return 0
		}
		goOn := true
		// No panic reaches the user: an internal error in an entry is reported
		// like any error, and the session goes on.
		if ie := source.Guard("mk repl entry", source.Span{}, func() { goOn = s.Entry(e, out, errw) }); ie != nil {
			fmt.Fprintf(errw, "mk repl: %s\n", ie.String())
		}
		if !goOn {
			return 0
		}
	}
}

// Start checks the package once with no entry: it reports the package's own
// diagnostics, which later entries do not repeat, and warms the cache.
func (s *Session) Start(errw io.Writer) {
	o, err := s.check(nil, "")
	if err == nil && s.needsMain(o) {
		// A directory of .mk files is a bin root, so with no Main nothing past
		// names is checked and no entry would get a value: the scratch file
		// declares an empty Main.
		s.mainStub = true
		o, err = s.check(nil, "")
	}
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	if len(o.own) > 0 {
		io.WriteString(errw, o.text(o.own))
	}
	if len(o.other) > 0 {
		fmt.Fprintf(errw, "mk repl: %d diagnostic(s) outside this session (the package's or std's); an entry that needs what they stop gets no value\n", len(o.other))
		io.WriteString(errw, o.text(o.other))
	}
}

// needsMain reports whether a check stopped on resolve's missing-Main E0201
// for the scratch file's own module: in the scratch file (it sorts first in
// its directory) or in another file of its directory. Another bin root's
// missing Main (a second pkg.Bin) is that module's error, and a stub would
// collide with the root module's own Main.
func (s *Session) needsMain(o *outcome) bool {
	missing := func(e diag.Emitted) bool {
		return e.Diag.Code == diag.E0201 && e.Diag.Message == "a program needs a `Main` function"
	}
	for _, e := range o.own {
		if missing(e) {
			return true
		}
	}
	sm := o.res.Sources
	if sm == nil {
		return false
	}
	dir, found := "", false
	for _, f := range sm.Files() {
		if f.Path == s.base || strings.HasSuffix(f.Path, "/"+s.base) {
			dir, found = path.Dir(f.Path), true
			break
		}
	}
	for _, e := range o.other {
		if !found || !missing(e) {
			continue
		}
		if f := sm.File(e.Diag.At.File); f != nil && path.Dir(f.Path) == dir {
			return true
		}
	}
	return false
}

// Entry runs one entry and reports whether the session goes on (false after
// :quit).
func (s *Session) Entry(entry string, out, errw io.Writer) bool {
	// Comments before an entry belong to it; an entry of comments alone is
	// nothing, not a declaration to keep.
	t := strings.TrimSpace(stripLeadingComments(entry))
	if t == "" {
		return true
	}
	if strings.HasPrefix(t, ":") {
		return s.command(t, out, errw)
	}
	s.n++
	n := s.n
	if strings.HasPrefix(t, "import ") || t == "import" {
		s.doImport(n, t, errw)
		return true
	}
	// A signature (a result type, flow or contract after the parameters) is
	// a function: checked once, as a declaration, not first as an
	// expression.
	if looksLikeDecl(entry) || isSignature(entry, false) {
		s.doDecl(n, entry, errw)
		return true
	}
	s.doExpr(n, entry, false, out, errw)
	return true
}

// piece is the entry being checked, after the session's declarations.
type piece struct {
	n    int
	kind pieceKind
	text string // the entry as typed
	name string // pieceExpr: the const's name
	imp  *imp   // pieceImport: the import under test
	// replace names the accepted declaration this one replaces ("" none).
	replace string
}

type pieceKind uint8

const (
	pieceExpr pieceKind = iota
	pieceDecl
	pieceImport
)

// outcome is one check of the scratch file.
type outcome struct {
	res   driver.Result
	ans   *driver.QueryAnswer
	own   []diag.Emitted // diagnostics located in the scratch file
	other []diag.Emitted // the package's and std's
	lr    *locRewriter
	// importAt maps a scratch line holding an import to the import's name.
	importAt map[int]string
}

// text renders ds as diagnostic text with the scratch file's locations
// rewritten.
func (o *outcome) text(ds []diag.Emitted) string {
	if len(ds) == 0 {
		return ""
	}
	r := diag.Report{Diags: ds}
	return o.lr.rewrite(r.Text(o.res.Sources))
}

// hasCode reports whether ds holds code c.
func hasCode(ds []diag.Emitted, c diag.Code) bool {
	for _, e := range ds {
		if e.Diag.Code == c {
			return true
		}
	}
	return false
}

// compose writes the scratch text for p (nil: the session alone). Imports
// are written only while their name is used ("name."), since an unused
// import is E0208 and stops evaluation; skip drops imports by name.
func (s *Session) compose(p *piece, skip map[string]bool) (string, []origin, map[int]string) {
	var b strings.Builder
	var orig []origin
	importAt := map[int]string{}
	line := func(l string, o origin) {
		b.WriteString(l)
		b.WriteByte('\n')
		orig = append(orig, o)
	}
	var body strings.Builder
	for _, d := range s.decls {
		if p != nil && p.replace != "" && d.name == p.replace {
			continue
		}
		body.WriteString(d.scratch())
		body.WriteByte('\n')
	}
	if p != nil && p.kind != pieceImport {
		body.WriteString(p.text)
	}
	used := body.String()
	line(scratchHeader, origin{})
	if s.mainStub {
		line("export Main(sys: &System) {", origin{})
		line("}", origin{})
	}
	for _, im := range s.imports {
		if p != nil && p.imp != nil && p.imp.name == im.name {
			continue // the import under test replaces it
		}
		if skip[im.name] || !uses(used, im.name) {
			continue
		}
		importAt[len(orig)+1] = im.name
		line(im.line, origin{im.n, 1})
	}
	if p != nil && p.imp != nil {
		importAt[len(orig)+1] = p.imp.name
		line(p.imp.line, origin{p.n, 1})
	}
	for _, d := range s.decls {
		if p != nil && p.replace != "" && d.name == p.replace {
			continue
		}
		for i, l := range strings.Split(d.scratch(), "\n") {
			line(l, origin{d.n, i + 1})
		}
		line("", origin{})
	}
	if p != nil {
		switch p.kind {
		case pieceDecl:
			for i, l := range strings.Split(p.text, "\n") {
				line(l, origin{p.n, i + 1})
			}
		case pieceExpr:
			line("const "+p.name+" =", origin{p.n, 0})
			ls := strings.Split(p.text, "\n")
			if s.fuel > 0 {
				ls[len(ls)-1] = withClause(ls[len(ls)-1], "fuel "+strconv.FormatUint(s.fuel, 10))
			}
			for i, l := range ls {
				line(l, origin{p.n, i + 1})
			}
		}
	}
	return b.String(), orig, importAt
}

// check composes, writes and checks the scratch file. With query set, the
// check is the `sig` query of that name (one Check, as mk query).
func (s *Session) check(p *piece, query string) (*outcome, error) {
	o, err := s.checkOnce(p, query, nil)
	if err != nil {
		return nil, err
	}
	// An import whose name appears only in a string or a comment is written
	// and is E0208: check once more without the imports E0208 named.
	skip := map[string]bool{}
	for _, e := range o.own {
		if e.Diag.Code != diag.E0208 {
			continue
		}
		ln := o.res.Sources.Position(e.Diag.At.StartPos()).Line
		if name, ok := o.importAt[int(ln)]; ok && (p == nil || p.imp == nil || p.imp.name != name) {
			skip[name] = true
		}
	}
	if len(skip) > 0 {
		return s.checkOnce(p, query, skip)
	}
	return o, nil
}

func (s *Session) checkOnce(p *piece, query string, skip map[string]bool) (*outcome, error) {
	text, orig, importAt := s.compose(p, skip)
	if err := s.write(text); err != nil {
		return nil, err
	}
	ds, err := driver.NewSession(s.cfg)
	if err != nil {
		return nil, err
	}
	o := &outcome{lr: newLocRewriter(s.base, orig), importAt: importAt}
	if query != "" {
		o.ans, o.res, err = ds.Query("sig", []string{query})
		if err != nil {
			return nil, err
		}
	} else {
		o.res = ds.Check()
	}
	for _, e := range o.res.Report.Diags {
		if s.inScratch(o.res.Sources, e.Diag.At) {
			o.own = append(o.own, e)
		} else {
			o.other = append(o.other, e)
		}
	}
	return o, nil
}

// inScratch reports whether at is in the scratch file.
func (s *Session) inScratch(sm *source.SourceMap, at source.Span) bool {
	if sm == nil || at.File == source.NoFile {
		return false
	}
	f := sm.File(at.File)
	return f != nil && (f.Path == s.base || strings.HasSuffix(f.Path, "/"+s.base))
}

// report prints an outcome's own diagnostics, or, when an entry got no
// value and has none, the package's.
func (s *Session) report(o *outcome, errw io.Writer) {
	if len(o.own) > 0 {
		io.WriteString(errw, o.text(o.own))
		if hasCode(o.own, diag.E0902) {
			fmt.Fprintf(errw, "    repl: raise this session's limit with :fuel <steps> (now %s)\n", s.fuelText())
		}
		return
	}
	if len(o.other) > 0 {
		io.WriteString(errw, "mk repl: diagnostics outside this session (the package's or std's) stop this entry:\n")
		io.WriteString(errw, o.text(o.other))
	}
}

func (s *Session) fuelText() string {
	if s.fuel == 0 {
		return strconv.FormatUint(types.DefaultFuel, 10) + ", the default"
	}
	return strconv.FormatUint(s.fuel, 10)
}

// syntaxInEntry reports whether an outcome holds an E0101 in entry n.
func syntaxInEntry(o *outcome, n int) bool {
	for _, e := range o.own {
		if e.Diag.Code != diag.E0101 {
			continue
		}
		ln := int(o.res.Sources.Position(e.Diag.At.StartPos()).Line)
		if ln >= 1 && ln <= len(o.lr.orig) && o.lr.orig[ln-1].n == n {
			return true
		}
		if ln > len(o.lr.orig) { // at the end of the file: the entry's last token
			return true
		}
	}
	return false
}

// doExpr evaluates an expression entry and prints its value (or, with
// typeOnly, its type). An expression that does not parse is tried as a
// declaration, since a function has no keyword.
func (s *Session) doExpr(n int, entry string, typeOnly bool, out, errw io.Writer) {
	name := "MkRepl" + strconv.Itoa(n)
	p := &piece{n: n, kind: pieceExpr, text: entry, name: name}
	o, err := s.check(p, name)
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	if !typeOnly && syntaxInEntry(o, n) {
		dp := s.declPiece(n, entry)
		od, err := s.check(dp, "")
		if err != nil {
			fmt.Fprintf(errw, "mk repl: %v\n", err)
			return
		}
		// A function whose body has a syntax error is still a function: its
		// declaration's diagnostics name the real error, the expression's
		// only the signature.
		if !syntaxInEntry(od, n) || looksLikeFunc(entry) {
			s.finishDecl(dp, od, errw)
			return
		}
	}
	if len(o.own) > 0 {
		s.report(o, errw)
		return
	}
	typ, val, ok := constAnswer(o.ans, name)
	if !ok {
		s.report(o, errw)
		if len(o.other) == 0 {
			fmt.Fprintln(errw, "mk repl: the entry has no value")
		}
		return
	}
	if typeOnly {
		fmt.Fprintln(out, typ)
		return
	}
	if typ == "void" {
		return
	}
	fmt.Fprintln(out, val) // printed as it is: a value holds no location
}

// constAnswer reads `const Name: T = V  (module m)` from a sig answer.
func constAnswer(a *driver.QueryAnswer, name string) (typ, val string, ok bool) {
	if a == nil || !a.Found {
		return "", "", false
	}
	head := "const " + name + ": "
	for _, l := range a.Answer {
		l = strings.TrimPrefix(l, "export ")
		if !strings.HasPrefix(l, head) {
			continue
		}
		l = l[len(head):]
		if i := strings.LastIndex(l, "  (module "); i >= 0 {
			l = l[:i]
		}
		t, v, has := strings.Cut(l, " = ")
		if !has {
			return t, "", false
		}
		return t, v, true
	}
	return "", "", false
}

func (s *Session) declPiece(n int, entry string) *piece {
	p := &piece{n: n, kind: pieceDecl, text: entry}
	if nm := declName(entry); nm != "" {
		for _, d := range s.decls {
			if d.name == nm {
				p.replace = nm
			}
		}
	}
	return p
}

// doDecl checks a declaration entry and keeps it when it is clean. A const
// is checked as the sig query of its name, so its value comes back with the
// check (finishDecl freezes it).
func (s *Session) doDecl(n int, entry string, errw io.Writer) {
	p := s.declPiece(n, entry)
	query := ""
	if name, _, ok := constDecl(entry); ok {
		query = name
	}
	o, err := s.check(p, query)
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	s.finishDecl(p, o, errw)
}

func (s *Session) finishDecl(p *piece, o *outcome, errw io.Writer) {
	if len(o.own) > 0 {
		s.report(o, errw)
		return
	}
	nm := declName(p.text)
	if p.replace != "" {
		kept := s.decls[:0]
		for _, d := range s.decls {
			if d.name != p.replace {
				kept = append(kept, d)
			}
		}
		s.decls = kept
	}
	d := decl{n: p.n, name: nm, text: p.text}
	if name, export, ok := constDecl(p.text); ok {
		if typ, val, ok := constAnswer(o.ans, name); ok && literalValue(typ, val) {
			d.frozen = "const " + name + ": " + typ + " = " + val
			if export {
				d.frozen = "export " + d.frozen
			}
		}
	}
	s.decls = append(s.decls, d)
}

// literalValue reports whether val, a value of type typ as the sig query
// spells it, reads back as the same value when written as a literal of typ:
// an integer (not the most negative of its type, whose literal overflows
// before the minus applies), a bool, or a char or str of printable
// characters, tabs and line ends (Go's quoting of anything else uses
// escapes Mk lacks). Floats are left out: an integral float prints with no
// point, and an integer literal does not adapt to a float type.
func literalValue(typ, val string) bool {
	switch typ {
	case "bool":
		return val == "true" || val == "false"
	case "char", "str":
		var u string
		if typ == "char" {
			if len(val) < 3 || val[0] != '\'' || val[len(val)-1] != '\'' {
				return false
			}
			r, _, tail, err := strconv.UnquoteChar(val[1:len(val)-1], '\'')
			if err != nil || tail != "" {
				return false
			}
			u = string(r)
		} else {
			var err error
			if u, err = strconv.Unquote(val); err != nil || val[0] != '"' {
				return false
			}
		}
		for _, r := range u {
			if r == utf8.RuneError || !unicode.IsPrint(r) && r != '\n' && r != '\r' && r != '\t' {
				return false
			}
		}
		return true
	case "int", "i8", "i16", "i32", "i64":
		bits := map[string]int{"int": 64, "i8": 8, "i16": 16, "i32": 32, "i64": 64}[typ]
		v, err := strconv.ParseInt(val, 10, 64)
		return err == nil && v != -1<<(bits-1)
	case "uint", "u8", "u16", "u32", "u64":
		_, err := strconv.ParseUint(val, 10, 64)
		return err == nil
	}
	return false
}

// doImport checks an import entry: it is kept when the scratch file's only
// diagnostic is the import's own E0208 (nothing uses it yet, so the module
// resolved) or none.
func (s *Session) doImport(n int, entry string, errw io.Writer) {
	name, line, ok := parseImport(entry)
	if !ok {
		s.n-- // not numbered: nothing was checked
		fmt.Fprintln(errw, "mk repl: an import entry is one line: import std.coll, or import c = std.coll")
		return
	}
	im := &imp{n: n, name: name, line: line}
	p := &piece{n: n, kind: pieceImport, imp: im}
	o, err := s.check(p, "")
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	var rest []diag.Emitted
	for _, e := range o.own {
		ln := int(o.res.Sources.Position(e.Diag.At.StartPos()).Line)
		if e.Diag.Code == diag.E0208 && o.importAt[ln] == name {
			continue
		}
		rest = append(rest, e)
	}
	if len(rest) > 0 {
		o.own = rest
		s.report(o, errw)
		return
	}
	kept := s.imports[:0]
	for _, x := range s.imports {
		if x.name != name {
			kept = append(kept, x)
		}
	}
	s.imports = append(kept, *im)
}

const helpText = `mk repl: entries run on the evaluator, as compile-time evaluation (no Io).
  <expression>          evaluate and print its value
  <declaration>         keep a function, struct, enum, error, interface, impl, const, var or test
                        (a const keeps the value it had when it was entered)
  import std.coll       import a std module or one of the package's (import src.util)
  :type <expression>    print the expression's type
  :query <q> [args]     ask mk query about the package and this session (mk query --help lists them)
  :fuel [steps|default] show or set the evaluation step limit (reference section 11)
  :decls                print the session's imports and declarations
  :reset                forget every import and declaration
  :help                 this text
  :quit                 end the session (so does the end of input)
`

// command runs a ':' command and reports whether the session goes on.
func (s *Session) command(t string, out, errw io.Writer) bool {
	cmd, arg, _ := strings.Cut(t[1:], " ")
	if i := strings.IndexAny(cmd, "\n\t"); i >= 0 {
		cmd, arg = cmd[:i], t[1+i+1:]
	}
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "q", "quit", "exit":
		return false
	case "help", "h", "?":
		io.WriteString(out, helpText)
	case "type", "t":
		if arg == "" {
			fmt.Fprintln(errw, "mk repl: :type needs an expression")
			return true
		}
		s.n++
		s.doExpr(s.n, arg, true, out, errw)
	case "query":
		s.doQuery(arg, out, errw)
	case "fuel":
		s.doFuel(arg, out, errw)
	case "decls":
		for _, im := range s.imports {
			fmt.Fprintln(out, im.line)
		}
		for _, d := range s.decls {
			fmt.Fprintln(out, d.text)
		}
	case "reset":
		s.imports, s.decls = nil, nil
	default:
		fmt.Fprintf(errw, "mk repl: unknown command :%s (:help lists them)\n", cmd)
	}
	return true
}

func (s *Session) doFuel(arg string, out, errw io.Writer) {
	switch arg {
	case "":
		fmt.Fprintln(out, s.fuelText())
		return
	case "default":
		s.fuel = 0
		return
	}
	v, err := strconv.ParseUint(strings.ReplaceAll(arg, "_", ""), 10, 64)
	if err != nil || v == 0 {
		fmt.Fprintf(errw, "mk repl: :fuel takes a positive number of steps or default, not %q\n", arg)
		return
	}
	s.fuel = v
}

// doQuery is mk query over the package and the session's declarations.
func (s *Session) doQuery(arg string, out, errw io.Writer) {
	f := strings.Fields(arg)
	if len(f) == 0 {
		fmt.Fprintln(errw, "mk repl: :query <query> [args]; the queries:")
		for _, q := range driver.QueryNames {
			fmt.Fprintln(errw, "  "+q.Help)
		}
		return
	}
	text, orig, _ := s.compose(nil, nil)
	if err := s.write(text); err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	ds, err := driver.NewSession(s.cfg)
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	ans, res, err := ds.Query(f[0], f[1:])
	if err != nil {
		fmt.Fprintf(errw, "mk repl: %v\n", err)
		return
	}
	lr := newLocRewriter(s.base, orig)
	if ans == nil {
		o := &outcome{res: res, lr: lr}
		for _, e := range res.Report.Diags {
			o.own = append(o.own, e)
		}
		io.WriteString(errw, o.text(o.own))
		fmt.Fprintf(errw, "mk repl: the package does not check far enough to answer %s\n", arg)
		return
	}
	io.WriteString(out, lr.rewrite(ans.Text()))
}
