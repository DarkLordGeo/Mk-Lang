// Why: generated code (`gen` output) has no file in the tree, yet definition must
// reach it and then the generator that made it.
//
// What: the gen view: the generated files written read-only under
// `<cache>/lsp/gen/<root hash>/`, and genLines, which maps a generated
// declaration back to its module's `gen` lines.
//
// How: a check's SourceMap holds each module's generated file at its virtual
// path `<module dir>/gen/<target>/gen.mk`; the server never writes `gen/`
// into the tree, so it copies the text under the cache and maps the paths.

package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"strings"

	"mk/internal/source"
)

// isGenPath reports whether a SourceMap path is a module's generated file.
func isGenPath(p string) bool {
	if strings.HasPrefix(p, "std/") || !strings.HasSuffix(p, "/gen.mk") {
		return false
	}
	return path.Base(path.Dir(path.Dir(p))) == "gen"
}

// genRel is a generated file's SourceMap path as a path under the gen
// directory: a single-file root's is absolute (`V:/x/gen/<target>/gen.mk`),
// so its volume and leading separator go.
func genRel(p string) string {
	fp := filepath.FromSlash(p)
	fp = strings.TrimPrefix(fp, filepath.VolumeName(fp))
	return strings.TrimLeft(fp, string(filepath.Separator))
}

// genDirOf is the directory the root's generated files are written under.
func (s *Server) genDirOf(r *root) string {
	cache := s.cacheDirOf(r)
	if cache == "" {
		return ""
	}
	h := sha256.Sum256([]byte(foldKey(r.key)))
	return filepath.Join(cache, "lsp", "gen", hex.EncodeToString(h[:])[:16])
}

// materializeGen writes the root's generated files of a check, read-only,
// replacing one whose text changed, and records them. Worker only.
func (s *Server) materializeGen(r *root, sm *source.SourceMap) {
	if sm == nil {
		return
	}
	dir := s.genDirOf(r)
	if dir == "" {
		return
	}
	r.genFiles = nil
	for _, f := range sm.Files() {
		if !isGenPath(f.Path) {
			continue
		}
		p := filepath.Join(dir, genRel(f.Path))
		if old, err := os.ReadFile(p); err != nil || string(old) != f.Text {
			if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
				continue
			}
			_ = os.Chmod(p, 0o644)
			if os.WriteFile(p, []byte(f.Text), 0o444) != nil {
				continue
			}
		}
		r.genFiles = append(r.genFiles, p)
	}
	r.genDir = dir
}

// genLines are the locations of the `gen` lines, in the module a
// generated file belongs to (the directory above its gen/<target>/), that
// emitted the declaration at l, then the generators they call. The trace
// comment above a generated item (`// gen derive.Eq#1 for User`, the
// generator's path and the line's first type argument; internal/gen
// output.go) picks the lines; with no trace, or none matching, every gen
// line of the module answers and no generator does.
func (s *Server) genLines(r *root, genFile string, l Location) []Location {
	rel, err := filepath.Rel(r.genDir, genFile)
	if err != nil {
		return nil
	}
	gpath, gfor := s.genTrace(genFile, l)
	modDir := filepath.Join(r.dir, filepath.Dir(filepath.Dir(filepath.Dir(rel))))
	if r.file != "" {
		modDir = r.dir // a single file is its module
	}
	var all, picked, gens []Location
	u8 := s.utf8.Load()
	for _, p := range s.rootFiles(r) {
		if pathKey(filepath.Dir(p)) != pathKey(modDir) {
			continue
		}
		f, ok := s.fileOf(p)
		if !ok {
			continue
		}
		text := f.text.s
		ct := code(f.toks)
		for _, ir := range items(text, ct) {
			if word(text, ct, ir[0]) != "gen" {
				continue
			}
			loc := Location{URI: s.uriOf(p), Range: f.text.span(ct[ir[0]].Start, ct[ir[1]].End, u8)}
			all = append(all, loc)
			path, first, k := genLineHead(text, ct, ir[0]+1, ir[1])
			if gpath == "" || path != gpath || gfor != "" && first != gfor {
				continue
			}
			picked = append(picked, loc)
			// The generator: the last segment of the path, declared in the
			// module the first segment's import binds.
			mod, ok := imports(text, ct)[word(text, ct, ir[0]+1)]
			if !ok || k < 0 {
				continue
			}
			name := lastSegment(path)
			top := func(d decl) bool { return d.Owner == "" && d.Name == name }
			if sd := s.stdModuleDir(mod); sd != "" {
				gens = append(gens, s.stdLocations(s.searchStd(sd, top))...)
			} else {
				for _, h := range s.searchModule(r, mod, top) {
					gens = append(gens, s.location(h))
				}
			}
		}
	}
	if len(picked) == 0 {
		return all
	}
	return append(picked, gens...)
}

// genLineHead reads a gen line's body ct[a..b]: the generator's path as
// the trace spells it (`derive.Eq`), its first argument's first word, and
// the index of the path's last token (-1 when there is no path). The
// argument is its last segment (`m.User` is `User`), as the trace's.
func genLineHead(text string, ct []token, a, b int) (string, string, int) {
	var parts []string
	k := -1
	j := a
	for ; j <= b && word(text, ct, j) != "("; j++ {
		parts = append(parts, word(text, ct, j))
		k = j
	}
	var arg []string
	for j++; j <= b && word(text, ct, j) != "," && word(text, ct, j) != ")" && word(text, ct, j) != "["; j++ {
		arg = append(arg, word(text, ct, j))
	}
	return strings.Join(parts, ""), lastSegment(strings.Join(arg, "")), k
}

// genTrace is the trace comment above the generated item at l: the
// generator's path and the `for` type, or "" for none.
func (s *Server) genTrace(genFile string, l Location) (string, string) {
	f, ok := s.fileOf(genFile)
	if !ok {
		return "", ""
	}
	off := f.text.off(l.Range.Start, s.utf8.Load())
	trace := ""
	for _, t := range f.toks {
		if t.Start >= off {
			break
		}
		if c := f.text.s[t.Start:t.End]; t.Kind == tComment && strings.HasPrefix(c, "// gen ") {
			trace = strings.TrimPrefix(c, "// gen ")
		}
	}
	head, forText, _ := strings.Cut(trace, " for ")
	path, _, _ := strings.Cut(head, "#")
	return strings.TrimSpace(path), lastSegment(strings.TrimSpace(forText))
}

// inGen reports whether a location is in one of the root's generated files.
func (s *Server) inGen(r *root, l Location) (string, bool) {
	p, ok := uriPath(l.URI)
	if !ok || r.genDir == "" {
		return "", false
	}
	return p, strings.HasPrefix(pathKey(p), pathKey(r.genDir)+string(filepath.Separator))
}
