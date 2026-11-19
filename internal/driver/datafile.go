// Why: compile-time code may read a data file (build.File, Gen.Input) and
// ask for the build profile (std.build's Profile); the evaluator cannot
// touch the file system, so the driver answers for it, and the answers must
// key the cache.
//
// What: program.ReadData and program.Profile (the evaluator's host
// callbacks), readDataFile, readTreeData, checkDataPath, dataRead and
// dataReadOf (the recorded reads), dataCache and world.dataPart.
//
// How: a data file is read under the package root of the reading module
// (the module holding the const initializer or the gen line): its
// tree's OS root, each path element Lstat'ed from there (a link, a
// non-regular file, or more than 64 MiB is refused). A memory tree (tests,
// corpus units) answers from its files. An embedded std module has no
// package root, so every read there is missing. Each package and path is
// read once per world, so every reader of a build gets the same bytes, and
// the read is recorded as a ReadData of the reading module (or gen line)
// with the bytes' SHA-256 under types.DataDomain, so an edit of the file
// reruns exactly what read it (frontWorld.Part answers the read on a
// later build).

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mk/internal/source"
	"mk/internal/types"
)

// dataCache holds a world's data file reads: one per tree and path.
type dataCache struct {
	mu sync.Mutex
	m  map[dataKey]types.DataFile
}

type dataKey struct {
	tree *srcTree
	path string
}

// checkDataPath reports whether path is a well-formed data file path: not
// empty, relative, with no drive, no `\`, and no `..`, `.` or empty
// element (DataFilePath's rule, which the source text alone decides).
func checkDataPath(path string) bool {
	if path == "" || path[0] == '/' || strings.ContainsAny(path, "\\:\x00") {
		return false
	}
	for el := range strings.SplitSeq(path, "/") {
		if el == "" || el == "." || el == ".." {
			return false
		}
	}
	return true
}

// dataRead is the ReadData read of path by the module at modulePath: the
// bytes' hash, or for a failed read the hash of its reason and path
// (not zero for every failure: a stored E0910 would keep reason missing
// after a directory appeared at the path).
func dataRead(modulePath, path string, data []byte, why types.DataFileError) types.Read {
	r := types.Read{Kind: types.ReadData, Key: types.ModuleKey(modulePath), Name: path}
	if why == types.DataFileOK {
		r.Hash = source.HashBytes(types.DataDomain, data)
	} else {
		r.Hash = source.HashBytes(types.DataFailDomain, []byte(why.String()+"\x00"+path))
	}
	return r
}

// readDataFile reads path of module m's package, under the rules above.
func (w *world) readDataFile(m *modNode, path string) types.DataFile {
	if !checkDataPath(path) {
		return types.DataFile{Err: types.DataFilePath}
	}
	if m == nil || m.std || m.tree == nil {
		return types.DataFile{Err: types.DataFileMissing, Read: dataReadOf(m, path, nil, types.DataFileMissing)}
	}
	k := dataKey{tree: m.tree, path: path}
	w.data.mu.Lock()
	if w.data.m == nil {
		w.data.m = map[dataKey]types.DataFile{}
	}
	got, ok := w.data.m[k]
	w.data.mu.Unlock()
	if !ok {
		got = readTreeData(m.tree, path)
		w.data.mu.Lock()
		if prev, dup := w.data.m[k]; dup {
			got = prev // another reader was first: every reader sees one answer
		} else {
			w.data.m[k] = got
		}
		w.data.mu.Unlock()
	}
	got.Read = dataReadOf(m, path, got.Bytes, got.Err)
	return got
}

func dataReadOf(m *modNode, path string, data []byte, why types.DataFileError) types.Read {
	if m == nil {
		return types.Read{}
	}
	return dataRead(m.path, path, data, why)
}

// readTreeData reads path of tree t, Lstat'ing each element from the root.
func readTreeData(t *srcTree, path string) types.DataFile {
	switch {
	case t.mem != nil:
		text, ok := t.mem[path]
		if !ok {
			return types.DataFile{Err: types.DataFileMissing}
		}
		if len(text) > types.DataFileLimit {
			return types.DataFile{Err: types.DataFileTooLarge}
		}
		return types.DataFile{Bytes: []byte(text)}
	case t.osRoot == "":
		return types.DataFile{Err: types.DataFileMissing}
	}
	p := t.osRoot
	els := strings.Split(path, "/")
	for i, el := range els {
		p = filepath.Join(p, el)
		fi, err := os.Lstat(p)
		if err != nil {
			return types.DataFile{Err: types.DataFileMissing}
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 && isReparse(p):
			return types.DataFile{Err: types.DataFileLink}
		case i < len(els)-1:
			if !fi.IsDir() {
				return types.DataFile{Err: types.DataFileMissing}
			}
		case !fi.Mode().IsRegular():
			return types.DataFile{Err: types.DataFileNotRegular}
		case fi.Size() > types.DataFileLimit:
			return types.DataFile{Err: types.DataFileTooLarge}
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return types.DataFile{Err: types.DataFileMissing}
	}
	if len(b) > types.DataFileLimit {
		return types.DataFile{Err: types.DataFileTooLarge}
	}
	return types.DataFile{Bytes: b}
}

// isReparse reports whether an irregular entry is a link: Go reports a
// Windows junction or other name-surrogate reparse point as ModeIrregular
// with no ModeSymlink, and a junction is a link for this rule.
func isReparse(p string) bool {
	_, err := os.Readlink(p)
	return err == nil
}

// ReadData implements types.DataFiles for the evaluator (build.File) and
// gen (Gen.Input).
func (p *program) ReadData(from types.ModuleID, path string) types.DataFile {
	var m *modNode
	if p.s != nil {
		m = p.s.module(from)
	}
	return p.w.readDataFile(m, path)
}

// Profile implements eval.BuildProfile: the build's profile, std.build's
// Profile. The front cache's context hashes the profile, so every
// module's key differs between profiles already.
func (p *program) Profile() types.Profile { return p.w.cfg.Profile }

// dataPart answers a ReadData read on a later build: the current bytes'
// hash, or the failure's hash while the read fails for the same reason;
// false when the module
// is not in this session.
func (w *world) dataPart(r types.Read) (source.Hash, bool) {
	var m *modNode
	if s := w.sem5; s != nil {
		s.mu.RLock()
		for _, x := range s.mods {
			if x.path == r.Key.String() {
				m = x
				break
			}
		}
		s.mu.RUnlock()
	}
	if m == nil {
		return source.Hash{}, false
	}
	df := w.readDataFile(m, r.Name)
	return df.Read.Hash, true
}
