// Why: the server must stay responsive while checks run, and never answer out of
// order or from two goroutines at once.
//
// What: Options, Server, Serve, the reader and worker loops, dispatch,
// initialize and Capabilities, and the document notifications.
//
// How: the reader applies document notifications at once and queues requests
// and checks; one worker answers them in order, because a driver Session is
// used from one goroutine and every check shares one cache directory.
// A panic in a handler answers InternalError and the server goes on. After
// shutdown and exit, the requests still queued are answered before Serve
// returns.

package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mk/internal/driver"
	"mk/internal/source"
)

// Options configure a server. The zero value uses the driver's defaults.
type Options struct {
	// StdDir and CacheDir pass to driver.Config: "" means MK_STD or the
	// embedded std, and MK_CACHE or <root>/.mk-cache.
	StdDir   string
	CacheDir string
	// Jobs bounds the driver's workers; <= 0 means every CPU.
	Jobs int
	// Log receives one line per message handled, with its wall time.
	Log io.Writer
}

// testBeforeJob, when set by a test, runs in the worker before it takes
// each job from the queue, so a test can hold the worker while it queues
// requests and cancels them.
var testBeforeJob func()

// job is one unit of the worker: a request (or a notification the worker
// handles), a check of one root, or the rechecks of the open roots that
// depend on one root through a path dependency.
type job struct {
	msg   *message
	check string // a root key
	deps  string // a root key whose open dependants are checked
}

// pendingKey is a check or dependants job's key in Server.pending, under
// which queued jobs of one root coalesce.
func (j job) pendingKey() string {
	switch {
	case j.check != "":
		return foldKey(j.check)
	case j.deps != "":
		return "deps:" + foldKey(j.deps)
	}
	return ""
}

// Server is one `mk lsp` process's state.
type Server struct {
	opt  Options
	out  *writer
	docs *store
	utf8 atomic.Bool
	// workDone: the client accepts window/workDoneProgress/create.
	workDone atomic.Bool
	// watchDynamic: the client registers file watchers dynamically.
	watchDynamic atomic.Bool
	// querySessions counts the sessions started only to answer a query,
	// each with its own check (exact.go answers without one).
	querySessions atomic.Int64
	// checks counts the Session.Check calls of checks (diagnostics, pull
	// diagnostics, on-demand full checks), and fullSessions the sessions
	// with Config.KeepSyntax; tests read them.
	checks, fullSessions atomic.Int64
	progressN            int // worker only
	requestN             int // worker only: server-to-client requests (showDocument)
	// semantic holds the last semantic tokens per document, for delta.
	semantic  map[string]semanticResult
	semanticN int

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []job
	pending   map[string]bool // roots with a queued, unstarted check
	cancelled map[string]bool // request ids
	closed    bool
	stopped   bool // the worker has returned
	// folders are the client's workspace folders and root URI as paths,
	// where a rename looks for dependant packages (rename2.go).
	folders []string

	// Worker-only state.
	roots map[string]*root
	files *fileCache
	// stdCopy is the materialised std (stdfiles.go); stdList its files.
	stdCopy, stdListDir string
	stdList             []string
	// explained maps a code to its explain file's URI ("" for none).
	explained map[string]string
	// stdKinds are std's type kinds from `mk query kind` (semtok.go).
	stdKinds map[string]string
	// known memoises knownTypes per root within one job (worker only).
	known map[*root]map[string]*typeInfo
}

// Serve runs a server over in and out until the client sends exit. It
// returns nil when exit followed shutdown, and an error when the stream
// ended, broke or exit came first (LSP 3.17: exit code 1).
func Serve(in io.Reader, out io.Writer, opt Options) error {
	return newServer(out, opt).run(in)
}

func newServer(out io.Writer, opt Options) *Server {
	s := &Server{opt: opt, out: &writer{w: out}, docs: newStore(),
		pending: map[string]bool{}, cancelled: map[string]bool{},
		roots: map[string]*root{}, files: newFileCache(), explained: map[string]string{}, semantic: map[string]semanticResult{}}
	s.cond = sync.NewCond(&s.mu)
	registerStdDir(opt.StdDir)
	return s
}

func (s *Server) run(in io.Reader) error {
	done := make(chan struct{})
	go s.work(done)
	err := s.read(bufio.NewReaderSize(in, 1<<16))
	s.mu.Lock()
	// After shutdown and exit, the requests still queued (shutdown's own
	// among them) are answered before the worker stops; queued checks are
	// dropped.
	if err == nil {
		var reqs []job
		for _, j := range s.queue {
			if j.msg != nil && j.msg.isRequest() {
				reqs = append(reqs, j)
			}
		}
		s.queue = reqs
		for len(s.queue) > 0 && !s.stopped {
			s.cond.Wait()
		}
	}
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	<-done
	return err
}

func (s *Server) logf(format string, args ...any) {
	if s.opt.Log != nil {
		fmt.Fprintf(s.opt.Log, format+"\n", args...)
	}
}

// read is the reader loop; it returns when the stream ends or exit comes.
func (s *Server) read(r *bufio.Reader) error {
	initialized, shutdown := false, false
	for {
		body, err := readFrame(r)
		if err != nil {
			if err == io.EOF {
				return errors.New("lsp: input ended before exit")
			}
			return err
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil || m.Method == "" && len(m.ID) == 0 {
			_ = s.out.fail(nil, codeParseError, "not a JSON-RPC message")
			continue
		}
		if m.Method == "" {
			continue // a response to a request the server never sends
		}
		switch {
		case m.Method == "exit":
			if !shutdown {
				return errors.New("lsp: exit before shutdown")
			}
			return nil
		case !initialized && m.Method != "initialize":
			if m.isRequest() {
				_ = s.out.fail(m.ID, codeServerNotInitialized, "the server is not initialized")
			}
			continue
		case shutdown && m.isRequest():
			_ = s.out.fail(m.ID, codeInvalidRequest, "the server is shut down")
			continue
		}
		switch m.Method {
		case "initialize":
			if initialized {
				_ = s.out.fail(m.ID, codeInvalidRequest, "initialize sent twice")
				continue
			}
			initialized = true
			s.initialize(&m)
			continue
		case "shutdown":
			shutdown = true
		case "$/cancelRequest":
			var p cancelParams
			if json.Unmarshal(m.Params, &p) == nil && p.ID != nil {
				s.mu.Lock()
				if len(s.cancelled) > 1024 {
					// Cancels of requests already answered never get
					// consumed; drop them in bulk.
					s.cancelled = map[string]bool{}
				}
				s.cancelled[idKey(p.ID)] = true
				s.mu.Unlock()
			}
			continue
		case "workspace/didChangeWatchedFiles":
			s.watched(&m)
			continue
		case "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose", "textDocument/didSave":
			s.document(&m)
			continue
		}
		if m.isRequest() || m.Method == "initialized" {
			s.enqueue(job{msg: &m})
		}
	}
}

func idKey(id any) string {
	switch v := id.(type) {
	case string:
		return "s:" + v
	case float64:
		return "n:" + strings.TrimSuffix(fmt.Sprintf("%f", v), ".000000")
	}
	return fmt.Sprint(id)
}

func rawIDKey(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	return idKey(v)
}

func (s *Server) enqueue(j job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k := j.pendingKey(); k != "" {
		if s.pending[k] {
			return
		}
		s.pending[k] = true
	}
	s.queue = append(s.queue, j)
	s.cond.Broadcast() // the worker, and run when it waits for the queue
}

// takePending removes a queued, unstarted check of the root key from the
// queue, so the worker can run it now in place of a check of its own;
// false when none is queued.
func (s *Server) takePending(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := foldKey(key)
	if !s.pending[k] {
		return false
	}
	for i, j := range s.queue {
		if j.check != "" && foldKey(j.check) == k {
			s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
			delete(s.pending, k)
			return true
		}
	}
	return false
}

// work is the worker loop.
func (s *Server) work(done chan struct{}) {
	defer close(done)
	defer func() {
		s.mu.Lock()
		s.stopped = true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	if ie := source.Guard("lsp worker", source.Span{}, func() {
		for {
			if testBeforeJob != nil {
				testBeforeJob()
			}
			s.mu.Lock()
			for len(s.queue) == 0 && !s.closed {
				s.cond.Wait()
			}
			if s.closed {
				s.mu.Unlock()
				return
			}
			j := s.queue[0]
			s.queue = s.queue[1:]
			if len(s.queue) == 0 {
				s.cond.Broadcast() // run may wait for the queue to empty
			}
			cancelled := false
			if k := j.pendingKey(); k != "" {
				delete(s.pending, k)
			} else if j.msg.isRequest() {
				k := rawIDKey(j.msg.ID)
				cancelled = s.cancelled[k]
				delete(s.cancelled, k)
			}
			s.mu.Unlock()
			s.known = nil // the type names are read again for each job
			switch {
			case j.check != "":
				s.runCheck(j.check)
			case j.deps != "":
				s.checkDependants(j.deps)
			case cancelled:
				_ = s.out.fail(j.msg.ID, codeRequestCancelled, "cancelled")
			default:
				s.handle(j.msg)
			}
		}
	}); ie != nil {
		s.logf("lsp: %s", ie.String())
	}
}

// handle answers one request under Guard: a panic is InternalError.
func (s *Server) handle(m *message) {
	start := time.Now()
	var result any
	var rerr *rpcError
	if ie := source.Guard("lsp "+m.Method, source.Span{}, func() {
		result, rerr = s.dispatch(m)
	}); ie != nil {
		rerr = &rpcError{Code: codeInternalError, Message: ie.String()}
	}
	s.logf("%s %v", m.Method, time.Since(start))
	if !m.isRequest() {
		return
	}
	if rerr != nil {
		_ = s.out.fail(m.ID, rerr.Code, rerr.Message)
		return
	}
	_ = s.out.reply(m.ID, result)
}

func invalid(err error) *rpcError { return &rpcError{Code: codeInvalidParams, Message: err.Error()} }

// decode reads params into v, or answers InvalidParams.
func decode(m *message, v any) *rpcError {
	if len(m.Params) == 0 {
		return &rpcError{Code: codeInvalidParams, Message: "missing params"}
	}
	if err := json.Unmarshal(m.Params, v); err != nil {
		return invalid(err)
	}
	return nil
}

// testPanicIn, when a test sets it, names a method whose handler panics,
// so the crash path is testable.
var testPanicIn string

func (s *Server) dispatch(m *message) (any, *rpcError) {
	if testPanicIn != "" && m.Method == testPanicIn {
		panic("test panic in " + m.Method)
	}
	switch m.Method {
	case "initialized":
		s.registerWatchers()
		return nil, nil
	case "shutdown":
		return nil, nil
	case "textDocument/hover":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.hover(p)
	case "textDocument/definition":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.definition(p)
	case "textDocument/references":
		var p referenceParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.references(p)
	case "textDocument/documentSymbol":
		var p documentParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.symbols(p)
	case "textDocument/formatting":
		var p documentParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.formatting(p)
	case "textDocument/completion":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.completion(p)
	case "textDocument/semanticTokens/full":
		var p documentParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.semanticTokens(p)
	case "textDocument/documentHighlight":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.highlights(p)
	case "textDocument/foldingRange":
		var p documentParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.foldingRanges(p)
	case "workspace/symbol":
		var p struct {
			Query string `json:"query"`
		}
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.workspaceSymbols(p.Query), nil
	case "textDocument/signatureHelp":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.signatureHelp(p)
	case "textDocument/diagnostic":
		var p documentParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.pullDiagnostics(p)
	case "textDocument/inlayHint":
		var p inlayHintParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.inlayHints(p)
	case "textDocument/typeDefinition":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.typeDefinition(p)
	case "textDocument/implementation":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.implementation(p)
	case "textDocument/prepareCallHierarchy":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.prepareCallHierarchy(p)
	case "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls":
		var p struct {
			Item callHierarchyItem `json:"item"`
		}
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		if m.Method == "callHierarchy/incomingCalls" {
			return s.incomingCalls(p.Item)
		}
		return s.outgoingCalls(p.Item)
	case "textDocument/selectionRange":
		var p selectionRangeParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.selectionRanges(p)
	case "completionItem/resolve":
		var p completionItem
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.resolveCompletion(p)
	case "textDocument/prepareRename":
		var p textDocumentPositionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.prepareRename(p)
	case "textDocument/rename":
		var p renameParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.rename(p)
	case "workspace/executeCommand":
		var p struct {
			Command   string `json:"command"`
			Arguments []any  `json:"arguments"`
		}
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.executeCommand(p)
	case "textDocument/rangeFormatting":
		var p rangeFormattingParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.rangeFormatting(p)
	case "textDocument/semanticTokens/full/delta":
		var p struct {
			TextDocument     textDocumentIdentifier `json:"textDocument"`
			PreviousResultID string                 `json:"previousResultId"`
		}
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.semanticTokensDelta(p)
	case "textDocument/codeAction":
		var p codeActionParams
		if e := decode(m, &p); e != nil {
			return nil, e
		}
		return s.codeActions(p)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
}

// initialize answers the capabilities and picks the position encoding.
func (s *Server) initialize(m *message) {
	var p initializeParams
	if len(m.Params) > 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			_ = s.out.fail(m.ID, codeInvalidParams, err.Error())
			return
		}
	}
	enc := "utf-16"
	for _, e := range p.Capabilities.General.PositionEncodings {
		if e == "utf-8" {
			enc = "utf-8"
		}
	}
	s.utf8.Store(enc == "utf-8")
	s.workDone.Store(p.Capabilities.Window.WorkDoneProgress)
	s.watchDynamic.Store(p.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration)
	var folders []string
	for _, u := range append([]string{p.RootURI}, folderURIs(p)...) {
		if path, ok := uriPath(u); ok && u != "" {
			folders = append(folders, path)
		}
	}
	s.mu.Lock()
	s.folders = folders
	s.mu.Unlock()
	_ = s.out.reply(m.ID, map[string]any{
		"capabilities": Capabilities(enc),
		"serverInfo":   map[string]any{"name": "mk lsp", "version": driver.Release},
	})
}

// Capabilities is the server's capabilities object for a position
// encoding ("utf-8" or "utf-16"): what `mk lsp --check-capabilities`
// prints, and what an editor client may rely on.
func Capabilities(enc string) map[string]any {
	return map[string]any{
		"positionEncoding":                enc,
		"textDocumentSync":                map[string]any{"openClose": true, "change": 2, "save": map[string]any{"includeText": false}},
		"diagnosticProvider":              map[string]any{"interFileDependencies": true, "workspaceDiagnostics": false},
		"hoverProvider":                   true,
		"definitionProvider":              true,
		"typeDefinitionProvider":          true,
		"implementationProvider":          true,
		"callHierarchyProvider":           true,
		"selectionRangeProvider":          true,
		"referencesProvider":              true,
		"renameProvider":                  map[string]any{"prepareProvider": true},
		"documentHighlightProvider":       true,
		"documentSymbolProvider":          true,
		"workspaceSymbolProvider":         true,
		"foldingRangeProvider":            true,
		"inlayHintProvider":               true,
		"documentFormattingProvider":      true,
		"documentRangeFormattingProvider": true,
		"completionProvider":              map[string]any{"triggerCharacters": []string{".", " "}, "resolveProvider": true},
		"signatureHelpProvider":           map[string]any{"triggerCharacters": []string{"(", ","}},
		"codeActionProvider":              map[string]any{"codeActionKinds": []string{"quickfix", "source.organizeImports"}},
		"executeCommandProvider":          map[string]any{"commands": []string{explainCommand}},
		"semanticTokensProvider": map[string]any{
			"legend": map[string]any{"tokenTypes": tokenLegend, "tokenModifiers": tokenModifiers},
			"full":   map[string]any{"delta": true},
		},
	}
}

// document applies a text-document notification in the reader, so every
// later request sees the new text, and queues the check.
func (s *Server) document(m *message) {
	u8 := s.utf8.Load()
	switch m.Method {
	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		path, ok := uriPath(p.TextDocument.URI)
		if !ok {
			return
		}
		s.docs.open(p.TextDocument.URI, path, p.TextDocument.Version, p.TextDocument.Text)
		// A buffer that differs from the disk changes what its dependants
		// read too.
		if disk, err := os.ReadFile(path); err != nil || string(disk) != p.TextDocument.Text {
			s.queueChange(path)
		} else {
			s.queueCheck(path)
		}
	case "textDocument/didChange":
		var p didChangeParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		path, ok := uriPath(p.TextDocument.URI)
		if !ok {
			return
		}
		d := s.docs.get(path)
		if d == nil {
			return
		}
		text := d.raw
		for _, c := range p.ContentChanges {
			if c.Range == nil {
				text = c.Text
				continue
			}
			l := newLines(text)
			a, b := l.off(c.Range.Start, u8), l.off(c.Range.End, u8)
			if a > b {
				a, b = b, a
			}
			text = text[:a] + c.Text + text[b:]
		}
		s.docs.open(d.uri, path, p.TextDocument.Version, text)
		s.queueChange(path)
	case "textDocument/didSave":
		var p didSaveParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		if path, ok := uriPath(p.TextDocument.URI); ok {
			s.queueCheck(path)
		}
	case "textDocument/didClose":
		var p didCloseParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		path, ok := uriPath(p.TextDocument.URI)
		if !ok {
			return
		}
		s.docs.close(path)
		// The closed buffer's diagnostics go with it. Its root and the
		// roots that depend on it are checked again, reading the file from
		// disk, so their diagnostics no longer come from the discarded
		// text. A root with no open file left only clears what it
		// published (runCheck).
		_ = s.out.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []lspDiagnostic{}})
		s.queueChange(path)
	}
}

func (s *Server) queueCheck(path string) {
	s.enqueue(job{check: rootKeyOf(path)})
}

// queueChange queues a check of the file's root after its text changed,
// and then the checks of the open roots that depend on that root.
func (s *Server) queueChange(path string) {
	k := rootKeyOf(path)
	s.enqueue(job{check: k})
	s.enqueue(job{deps: k})
}
