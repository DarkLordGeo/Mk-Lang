// Why: every request is tested the way an editor drives the server.
// What: the scripted-session client (start, call, notify, diagnostics, shutdown)
// and the fixture package.
// How: the server runs over two pipes, as over stdio; one module cache is shared
// by every test of the package, so std is checked cold once.

package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCache is one module cache for every test of the package, so std is
// checked cold once.
var testCache string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mk-lsp-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	testCache = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// waitFor bounds every wait of the harness: a cold std check under load
// takes seconds, never minutes.
const waitFor = 90 * time.Second

type client struct {
	t      *testing.T
	w      *io.PipeWriter
	msgs   chan message
	served chan error
	srv    *Server
	id     int
	notes  []message
	// seen is every message from the server with a method, in order,
	// whether or not a wait consumed it.
	seen []message
}

// start runs a server and returns its client. The cleanup closes the input
// and waits for the server.
func start(t *testing.T) *client {
	t.Helper()
	return startWith(t, Options{CacheDir: testCache, Jobs: 4})
}

// startWith is start with the server's options.
func startWith(t *testing.T, opt Options) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, w: inW, msgs: make(chan message, 64), served: make(chan error, 1)}
	c.srv = newServer(outW, opt)
	go func() {
		err := c.srv.run(inR)
		outW.Close()
		c.served <- err
	}()
	go func() {
		br := bufio.NewReader(outR)
		for {
			body, err := readFrame(br)
			if err != nil {
				close(c.msgs)
				return
			}
			var m message
			if err := json.Unmarshal(body, &m); err != nil {
				panic(err)
			}
			c.msgs <- m
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		drained := make(chan struct{})
		go func() {
			for range c.msgs {
			}
			close(drained)
		}()
		select {
		case <-c.served:
		case <-time.After(waitFor):
			t.Error("server did not stop")
		}
		<-drained
	})
	return c
}

func (c *client) raw(body string) {
	c.t.Helper()
	if _, err := io.WriteString(c.w, "Content-Length: "+fmt.Sprint(len(body))+"\r\n\r\n"+body); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.raw(string(b))
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// next is the next message from the server.
func (c *client) next() message {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if !ok {
			c.t.Fatal("server output ended")
		}
		if m.Method != "" {
			c.seen = append(c.seen, m)
		}
		return m
	case <-time.After(waitFor):
		c.t.Fatal("no message from the server")
	}
	return message{}
}

// call sends a request and returns its response, keeping notifications.
func (c *client) call(method string, params any) message {
	c.t.Helper()
	c.id++
	id := c.id
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return c.response(id)
}

func (c *client) response(id int) message {
	c.t.Helper()
	for {
		m := c.next()
		if m.Method != "" {
			c.notes = append(c.notes, m)
			continue
		}
		if string(m.ID) == fmt.Sprint(id) {
			return m
		}
		c.t.Fatalf("response to %s while waiting for %d", m.ID, id)
	}
}

// result decodes a successful response.
func (c *client) result(m message, v any) {
	c.t.Helper()
	if m.Error != nil {
		c.t.Fatalf("error response: %v", m.Error)
	}
	if err := json.Unmarshal(m.Result, v); err != nil {
		c.t.Fatalf("result %s: %v", m.Result, err)
	}
}

func (c *client) init(utf8 bool) map[string]any {
	c.t.Helper()
	params := map[string]any{"capabilities": map[string]any{}}
	if utf8 {
		params["capabilities"] = map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}}}
	}
	var res map[string]any
	c.result(c.call("initialize", params), &res)
	c.notify("initialized", map[string]any{})
	return res
}

// diagnostics waits for the next publishDiagnostics of uri.
func (c *client) diagnostics(uri string) publishDiagnosticsParams {
	c.t.Helper()
	for {
		var m message
		if len(c.notes) > 0 {
			m, c.notes = c.notes[0], c.notes[1:]
		} else {
			m = c.next()
		}
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p publishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			c.t.Fatal(err)
		}
		if sameURI(p.URI, uri) {
			return p
		}
	}
}

func (c *client) open(uri, text string) {
	c.t.Helper()
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "mk", "version": 1, "text": text}})
}

func (c *client) change(uri string, version int, text string) {
	c.t.Helper()
	c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{"text": text}}})
}

func (c *client) shutdown() {
	c.t.Helper()
	if m := c.call("shutdown", nil); m.Error != nil {
		c.t.Fatalf("shutdown: %v", m.Error)
	}
	c.notify("exit", nil)
	select {
	case err := <-c.served:
		if err != nil {
			c.t.Fatalf("Serve after shutdown and exit: %v", err)
		}
		c.served <- nil
	case <-time.After(waitFor):
		c.t.Fatal("server did not exit")
	}
}

func pos(line, char int) map[string]any { return map[string]any{"line": line, "character": char} }

func at(uri string, line, char int) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos(line, char)}
}

func doc(uri string) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}}
}

// fixture is a package with two modules: main.mk at the root and util.
const fixtureManifest = `const Package = pkg.Package(
    name: "fx",
    bins: [pkg.Bin(name: "fx", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

const fixtureMain = `import util

/// Pair holds two ints.
export struct Pair {
    export lo: int
    export hi: int
}

/// Width is hi minus lo.
export Pair.Width(&self): int {
    return self.hi - self.lo
}

export enum Kind {
    Small(n: int)
    Zero
}

Sum(xs: []int): int {
    var t := 0
    for x in xs {
        t += x
    }
    return t
}

export Main(sys: &System) {
    p := Pair(lo: 1, hi: 3)
    _ = p.Width()
    _ = util.Twice(Sum([1, 2][0..2]))
}
`

const fixtureUtil = `/// Twice doubles n.
export Twice(n: int): int {
    return n * 2
}
`

// writeFixture writes the package and returns its directory and the URIs
// of main.mk and util/util.mk.
func writeFixture(t *testing.T) (dir, mainURI, utilURI string) {
	t.Helper()
	dir = t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "util"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(fixtureManifest), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "main.mk"), []byte(fixtureMain), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "util", "util.mk"), []byte(fixtureUtil), 0o644))
	return dir, pathURI(filepath.Join(dir, "main.mk")), pathURI(filepath.Join(dir, "util", "util.mk"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// lineOf is the zero-based line of the first line of text containing sub,
// and the byte column of sub in it.
func lineOf(t *testing.T, text, sub string) (int, int) {
	t.Helper()
	for i, l := range strings.Split(text, "\n") {
		if j := strings.Index(l, sub); j >= 0 {
			return i, j
		}
	}
	t.Fatalf("%q not in the text", sub)
	return 0, 0
}

func writeFile(path, text string) error { return os.WriteFile(path, []byte(text), 0o644) }
