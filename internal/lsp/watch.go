// Why: files change on disk behind the editor (git, generators, other tools).
//
// What: didChangeWatchedFiles handling and registerWatchers.
//
// How: a changed file queues a check of every root it belongs to and of the open
// roots that depend on it; a changed pkg.mk re-roots the open files below
// it. The server asks for `**/*.mk` when the client registers watchers
// dynamically.

package lsp

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type fileEvent struct {
	URI  string `json:"uri"`
	Type int    `json:"type"` // 1 created, 2 changed, 3 deleted
}

// watched applies a didChangeWatchedFiles notification in the reader.
func (s *Server) watched(m *message) {
	var p struct {
		Changes []fileEvent `json:"changes"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	keys := map[string]string{}
	changed := map[string]string{} // roots whose dependants are checked
	for _, ch := range p.Changes {
		path, ok := uriPath(ch.URI)
		if !ok || !strings.HasSuffix(path, ".mk") {
			continue
		}
		if filepath.Base(path) == "pkg.mk" {
			// The manifest: every open file below its directory, whose
			// root may have changed with it.
			dir := pathKey(filepath.Dir(path)) + string(filepath.Separator)
			for _, d := range s.docs.all() {
				if strings.HasPrefix(pathKey(d.path), dir) {
					k := rootKeyOf(d.path)
					keys[foldKey(k)] = k
				}
			}
			continue
		}
		k := rootKeyOf(path)
		// The open roots that depend on this one read the file too, open
		// or not.
		changed[foldKey(k)] = k
		// Only a root with an open file is checked: nothing else is shown.
		for _, d := range s.docs.all() {
			if foldKey(rootKeyOf(d.path)) == foldKey(k) {
				keys[foldKey(k)] = k
				break
			}
		}
	}
	// In key order: map order never decides which root is checked first.
	for _, fk := range sortedKeys(keys) {
		s.enqueue(job{check: keys[fk]})
	}
	for _, fk := range sortedKeys(changed) {
		s.enqueue(job{deps: changed[fk]})
	}
}

// registerWatchers asks the client to report changes to .mk files, when it
// registers didChangeWatchedFiles dynamically (LSP 3.17).
func (s *Server) registerWatchers() {
	if !s.watchDynamic.Load() {
		return
	}
	_ = s.out.send(map[string]any{"jsonrpc": "2.0", "id": "mk-watch", "method": "client/registerCapability",
		"params": map[string]any{"registrations": []any{map[string]any{
			"id": "mk-watch", "method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{"watchers": []any{map[string]any{"globPattern": "**/*.mk"}}},
		}}}})
}
