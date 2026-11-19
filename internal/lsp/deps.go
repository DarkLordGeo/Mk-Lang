// Why: workspace symbols and definition must reach a package's dependencies, which
// may live beside it, in vendor/ or only in the module cache.
//
// What: the dependency view: manifestDeps reads pkg.mk lexically, vendorIndexOf
// reads vendor/index.txt, and materializeDeps writes fetched dependency files
// read-only under `<cache>/lsp/deps/`.
//
// How: a check's SourceMap holds every dependency file it loaded: a path
// dependency's under its relative path, a vendored one's under
// `vendor/<canonical id>/` (`vendor/<name>/` in the older layout), a fetched
// one's under `<canonical id>@<12 hex>/`, which exists only in memory, so it
// is written out. No driver export is needed.

package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"mk/internal/source"
)

// depsDirOf is where fetched dependencies' files are written. Their paths
// carry the archive's hash, so one directory serves every root.
func (s *Server) depsDirOf(r *root) string {
	cache := s.cacheDirOf(r)
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "lsp", "deps")
}

// isFetchedPath reports whether a SourceMap path is a fetched
// dependency's: its first segment is `<canonical id>@<hash>`.
func isFetchedPath(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return strings.Contains(first, "@")
}

// materializeDeps records the root's dependency files of a check, writes
// the fetched ones, and maps each dependency name to the directory of its
// root module. Worker only.
func (s *Server) materializeDeps(r *root, sm *source.SourceMap) {
	if sm == nil || r.file != "" {
		return
	}
	r.depFiles, r.depDirs = nil, map[string]string{}
	var firsts []string // the fetched trees' first segments
	for _, f := range sm.Files() {
		p := f.Path
		if strings.HasPrefix(p, "std/") || isGenPath(p) || filepath.IsAbs(filepath.FromSlash(p)) || path.Base(p) == "pkg.mk" || !strings.HasSuffix(p, ".mk") {
			continue
		}
		var osPath string
		switch {
		case isFetchedPath(p):
			dir := s.depsDirOf(r)
			if dir == "" {
				continue
			}
			osPath = filepath.Join(dir, filepath.FromSlash(p))
			if old, err := os.ReadFile(osPath); err != nil || string(old) != f.Text {
				if os.MkdirAll(filepath.Dir(osPath), 0o755) != nil {
					continue
				}
				_ = os.Chmod(osPath, 0o644)
				if os.WriteFile(osPath, []byte(f.Text), 0o444) != nil {
					continue
				}
			}
			first, _, _ := strings.Cut(p, "/")
			firsts = append(firsts, first)
		case strings.HasPrefix(p, "../") || strings.HasPrefix(p, "vendor/"):
			osPath = filepath.Join(r.dir, filepath.FromSlash(p))
		default:
			continue
		}
		r.depFiles = append(r.depFiles, osPath)
	}
	index := vendorIndexOf(r.dir)
	urls := s.manifestDepArg(r, "url")
	sources, versions := s.manifestDepArg(r, "source"), s.manifestDepArg(r, "version")
	for name, rel := range s.manifestDeps(r) {
		if src := sources[name]; src != "" {
			// A source package: vendor/<canonical id> through
			// the index, else its fetched tree `<canonical id>@<hash>/`.
			cid := sourceCIDOf(src, versions[name])
			if major, ok := majorOf("v" + versions[name]); ok && index[src+"@v"+major] == cid {
				if d := filepath.Join(r.dir, "vendor", cid); s.hasDepFileUnder(r, d) {
					r.depDirs[name] = d
					continue
				}
			}
			for _, f := range firsts {
				if strings.HasPrefix(f, cid+"@") {
					r.depDirs[name] = filepath.Join(s.depsDirOf(r), f)
					break
				}
			}
			continue
		}
		// 1.3's layout is vendor/<canonical id>, found in vendor/index.txt
		// by the path or url; 1.2's was vendor/<name>.
		vendored := filepath.Join(r.dir, "vendor", name)
		key := urls[name]
		if rel != "" {
			key = path.Clean(rel)
		}
		if cid := index[key]; cid != "" {
			vendored = filepath.Join(r.dir, "vendor", cid)
		} else if rel == "" && urls[name] != "" {
			// The index writes a url without its credentials (the
			// driver's redactURL), so a url package is found by the
			// canonical id its whole url gives.
			if d := filepath.Join(r.dir, "vendor", urlCIDOf(urls[name])); s.hasDepFileUnder(r, d) {
				vendored = d
			}
		}
		switch {
		case s.hasDepFileUnder(r, vendored):
			r.depDirs[name] = vendored
		case rel != "":
			r.depDirs[name] = filepath.Join(r.dir, filepath.FromSlash(rel))
		default:
			// A fetched tree is `<canonical id>@<hash>/`.
			cid := urlCIDOf(urls[name])
			for _, f := range firsts {
				if strings.HasPrefix(f, cid+"@") {
					r.depDirs[name] = filepath.Join(s.depsDirOf(r), f)
					break
				}
			}
		}
	}
}

func (s *Server) hasDepFileUnder(r *root, dir string) bool {
	k := pathKey(dir) + string(filepath.Separator)
	for _, p := range r.depFiles {
		if strings.HasPrefix(pathKey(p), k) {
			return true
		}
	}
	return false
}

// manifestDeps reads the root's pkg.mk for its dependencies: name to
// path ("" for a url dependency).
func (s *Server) manifestDeps(r *root) map[string]string { return s.manifestDepArg(r, "path") }

// manifestDepArg reads the root's pkg.mk for its dependencies: name to
// the string argument arg ("" when the Dep does not write it).
func (s *Server) manifestDepArg(r *root, arg string) map[string]string {
	out := map[string]string{}
	f, ok := s.fileOf(filepath.Join(r.dir, "pkg.mk"))
	if !ok {
		return out
	}
	text := f.text.s
	ct := code(f.toks)
	for k := 0; k+1 < len(ct); k++ {
		if word(text, ct, k) != "Dep" || word(text, ct, k+1) != "(" {
			continue
		}
		end := matching(text, ct, k+1)
		args := map[string]string{}
		for j := k + 2; j+2 <= end; j++ {
			if isIdent(ct, j) && word(text, ct, j+1) == ":" && ct[j+2].Kind == tString {
				if v, err := strconv.Unquote(word(text, ct, j+2)); err == nil {
					args[word(text, ct, j)] = v
				}
			}
		}
		if args["name"] != "" {
			out[args["name"]] = args[arg]
		}
		k = end
	}
	return out
}

// vendorIndexOf reads dir's vendor/index.txt: the canonical id of each vendored package by its first
// field, a path relative to dir or a url.
func vendorIndexOf(dir string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(dir, "vendor", "index.txt"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		key := f[0]
		if major, ok := majorOf(f[1]); ok {
			// A source package's line (1.3): its id hashes the source and
			// major, and it is found by them.
			key = f[0] + "@v" + major
		}
		if vendorIDOf(key, f[2]) {
			out[key] = f[2]
		}
	}
	return out
}

// majorOf is the major of an index line's version field "vX.Y.Z"; false
// for "-" (a path or url package).
func majorOf(v string) (string, bool) {
	rest, ok := strings.CutPrefix(v, "v")
	if !ok {
		return "", false
	}
	major, _, ok := strings.Cut(rest, ".")
	return major, ok && major != ""
}

// sourceCIDOf is a source package's canonical id as the driver forms it
// (internal/driver sourceID): the source's last element made an
// identifier, "_", and 8 hex of SHA-256 over "<source>@v<major>".
func sourceCIDOf(source, version string) string {
	major, _, _ := strings.Cut(version, ".")
	key := source + "@v" + major
	b := []byte(strings.ToLower(path.Base(source)))
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	if len(b) == 0 || b[0] >= '0' && b[0] <= '9' {
		b = append([]byte{'_'}, b...)
	}
	sum := sha256.Sum256([]byte(key))
	return string(b) + "_" + hex.EncodeToString(sum[:4])
}

// urlCIDOf is a url package's canonical id as the driver forms it
// (internal/driver urlCID): the url path's last element without its
// archive extension, made an identifier, "_", and 8 hex of SHA-256 over
// the url.
func urlCIDOf(raw string) string {
	name := "pkg"
	if u, err := url.Parse(raw); err == nil {
		base := path.Base(u.Path)
		for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
			if b, ok := strings.CutSuffix(strings.ToLower(base), ext); ok {
				base = base[:len(b)]
				break
			}
		}
		if base != "" && base != "." && base != "/" {
			name = base
		}
	}
	b := []byte(strings.ToLower(name))
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	if len(b) == 0 || b[0] >= '0' && b[0] <= '9' {
		b = append([]byte{'_'}, b...)
	}
	sum := sha256.Sum256([]byte(raw))
	return string(b) + "_" + hex.EncodeToString(sum[:4])
}

// vendorIDOf reports whether id is a canonical id the index may name for
// key (as the driver checks it): `<identifier>_<8 hex>`,
// the hex the first 4 bytes of SHA-256 over key, a path key case-folded on
// Windows. Any other id is ignored, so it never reaches a file path.
func vendorIDOf(key, id string) bool {
	i := strings.LastIndexByte(id, '_')
	if i <= 0 {
		return false
	}
	for _, c := range []byte(id[:i]) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	if !strings.Contains(key, "://") && runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return id[i+1:] == hex.EncodeToString(sum[:4])
}

// searchModule are the declarations of one module of the package or of
// a dependency (`util.src` is the module src of the dependency util).
func (s *Server) searchModule(r *root, mod string, pred func(decl) bool) []hit {
	first, rest, _ := strings.Cut(mod, ".")
	if dir, ok := r.depDirs[first]; ok {
		if rest != "" {
			dir = joinModule(dir, rest)
		}
		k := pathKey(dir)
		var out []hit
		for _, h := range s.searchPaths(r.depFiles, pred) {
			if pathKey(filepath.Dir(h.f.path)) == k {
				out = append(out, h)
			}
		}
		return out
	}
	k := pathKey(joinModule(r.dir, mod))
	var out []hit
	for _, h := range s.search(r, pred) {
		if pathKey(filepath.Dir(h.f.path)) == k {
			out = append(out, h)
		}
	}
	return out
}
