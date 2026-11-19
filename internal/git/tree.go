// Why: a package's files come from the tree of its tag's commit; the walk
// must refuse what mk will not unpack and bound what a hostile tree can
// expand to.
//
// What: TreeEntry, ParseTree, ParseCommit, ParseTag, File, and Store's Peel,
// Files and Lookup.
//
// How: the commit a tag names gives the tree, and the tree is walked
// into regular files. Entries 100644 and 100755 are files (100664, which
// old git wrote, reads as 100644), 40000 directories (040000 accepted
// too); 120000 (a link) and 160000 (a submodule) are refused, as is any
// other mode and the name ".git" in any case. A name is at most 4,096
// bytes, as git's fsck holds it. The Limits' directory depth, entry
// count and byte limits (file bytes and path bytes) are counted during the
// walk, since a tree that names one subtree many times expands far beyond
// the pack.

package git

import (
	"bytes"
	"strings"
)

// File modes of tree entries.
const (
	ModeFile       uint32 = 0o100644
	ModeExecutable uint32 = 0o100755
	ModeDir        uint32 = 0o40000
	ModeLink       uint32 = 0o120000
	ModeSubmodule  uint32 = 0o160000
	modeGroupFile  uint32 = 0o100664
)

// TreeEntry is one entry of a tree object.
type TreeEntry struct {
	Mode uint32
	Name string
	OID  OID
}

// MaxNameLen caps a tree entry's name, as git's fsck does (4,096 bytes).
const MaxNameLen = 4096

// ParseTree reads a tree object: entries "<octal mode> <name>\0<20-byte
// id>". A name is not empty, ".", ".." and holds no '/'; two entries of one
// name are refused. It allocates in proportion to data; the walk uses
// eachEntry, which charges each entry before it keeps anything.
func ParseTree(data []byte) ([]TreeEntry, error) {
	var out []TreeEntry
	seen := map[string]bool{}
	err := eachEntry(data, func(e TreeEntry) error {
		if seen[e.Name] {
			return corrupt("a tree holds two entries named %s", clean(e.Name))
		}
		seen[e.Name] = true
		out = append(out, e)
		return nil
	})
	return out, err
}

// eachEntry parses data one entry at a time and calls visit for each,
// before parsing the next. No name over MaxNameLen is copied.
func eachEntry(data []byte, visit func(TreeEntry) error) error {
	for pos := 0; pos < len(data); {
		sp := bytes.IndexByte(data[pos:min(len(data), pos+8)], ' ')
		if sp <= 0 {
			return corrupt("a tree entry's mode is malformed")
		}
		var mode uint32
		for _, c := range data[pos : pos+sp] {
			if c < '0' || c > '7' {
				return corrupt("a tree entry's mode %s is not octal", clean(string(data[pos:pos+sp])))
			}
			mode = mode<<3 | uint32(c-'0')
		}
		pos += sp + 1
		nul := bytes.IndexByte(data[pos:min(len(data), pos+MaxNameLen+1)], 0)
		if nul < 0 {
			if len(data)-pos > MaxNameLen {
				return corrupt("a tree entry's name is longer than %d bytes", MaxNameLen)
			}
			return corrupt("a tree entry's name is not terminated")
		}
		raw := data[pos : pos+nul]
		pos += nul + 1
		if len(raw) == 0 || string(raw) == "." || string(raw) == ".." || bytes.IndexByte(raw, '/') >= 0 {
			return corrupt("a tree entry has the name %s", clean(string(raw)))
		}
		if pos+20 > len(data) {
			return corrupt("the tree entry %s is cut short", clean(string(raw)))
		}
		var o OID
		copy(o[:], data[pos:pos+20])
		pos += 20
		if err := visit(TreeEntry{Mode: mode, Name: string(raw), OID: o}); err != nil {
			return err
		}
	}
	return nil
}

// ParseCommit returns the tree a commit object names on its first line.
func ParseCommit(data []byte) (OID, error) {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	hexID, ok := bytes.CutPrefix(line, []byte("tree "))
	if !ok {
		return OID{}, corrupt("a commit does not start with its tree")
	}
	o, ok := ParseOID(string(hexID))
	if !ok {
		return OID{}, corrupt("a commit's tree id %q is malformed", hexID)
	}
	return o, nil
}

// ParseTag returns the object an annotated tag names and that object's
// type, from its "object" and "type" header lines.
func ParseTag(data []byte) (OID, string, error) {
	var obj OID
	var typ string
	haveObj := false
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if len(line) == 0 {
			break // the header ends at the first empty line
		}
		if v, ok := bytes.CutPrefix(line, []byte("object ")); ok && !haveObj {
			o, ok := ParseOID(string(v))
			if !ok {
				return OID{}, "", corrupt("a tag's object id %q is malformed", v)
			}
			obj, haveObj = o, true
		} else if v, ok := bytes.CutPrefix(line, []byte("type ")); ok && typ == "" {
			typ = string(v)
		}
	}
	if !haveObj || typ == "" {
		return OID{}, "", corrupt("a tag lacks its object or type line")
	}
	return obj, typ, nil
}

// File is one regular file of a walked tree: its '/'-separated path from
// the root, its mode (ModeFile or ModeExecutable) and its bytes.
type File struct {
	Path string
	Mode uint32
	Data []byte
}

// maxTagDepth bounds a tag of a tag of ... a commit.
const maxTagDepth = 16

// Peel follows annotated tags from o to the commit they name.
func (s *Store) Peel(o OID) (OID, error) {
	for range maxTagDepth {
		obj, ok := s.objs[o]
		if !ok {
			return OID{}, corrupt("the object %s is not in the pack", o)
		}
		switch obj.Type {
		case ObjCommit:
			return o, nil
		case ObjTag:
			next, _, err := ParseTag(obj.Data)
			if err != nil {
				return OID{}, err
			}
			o = next
		default:
			return OID{}, corrupt("the ref names a %s, not a commit", obj.Type)
		}
	}
	return OID{}, corrupt("tags nest deeper than %d", maxTagDepth)
}

// Files walks the tree of the commit c into its regular files, in tree
// order, under lim's directory depth, file count and byte limits.
func (s *Store) Files(c OID, lim Limits) ([]File, error) {
	lim = lim.withDefaults()
	obj, ok := s.objs[c]
	if !ok || obj.Type != ObjCommit {
		return nil, corrupt("the commit %s is not in the pack", c)
	}
	root, err := ParseCommit(obj.Data)
	if err != nil {
		return nil, err
	}
	w := &walker{s: s, lim: lim, bytes: lim.MaxBytes}
	if err := w.tree(root, "", 0); err != nil {
		return nil, err
	}
	return w.files, nil
}

type walker struct {
	s       *Store
	lim     Limits
	files   []File
	entries int   // entries visited
	bytes   int64 // left
}

func (w *walker) tree(o OID, prefix string, depth int) error {
	obj, ok := w.s.objs[o]
	if !ok || obj.Type != ObjTree {
		return corrupt("the tree %s of %s is not in the pack", o, clean(strings.TrimSuffix(prefix, "/")))
	}
	seen := map[string]bool{}
	return eachEntry(obj.Data, func(e TreeEntry) error {
		// Every entry visited, a directory as a file, counts against the
		// entry limit, and its path against the byte limit, before
		// anything is kept: a tree that names one empty or long-named
		// subtree twice per level is exponential in the pack's size.
		if w.entries >= w.lim.MaxFiles {
			return errorf(ReasonLimit, "the tree holds more than %d entries", w.lim.MaxFiles)
		}
		w.entries++
		if int64(len(prefix)+len(e.Name)) > w.bytes {
			return errorf(ReasonLimit, "the tree's files and paths hold more than %d bytes", w.lim.MaxBytes)
		}
		w.bytes -= int64(len(prefix) + len(e.Name))
		if seen[e.Name] {
			return corrupt("a tree holds two entries named %s", clean(e.Name))
		}
		seen[e.Name] = true
		p := prefix + e.Name
		if strings.EqualFold(e.Name, ".git") {
			return errorf(ReasonRefused, "the tree entry %s is refused: a .git entry is not unpacked", clean(p))
		}
		switch e.Mode {
		case ModeFile, ModeExecutable, modeGroupFile:
			blob, ok := w.s.objs[e.OID]
			if !ok || blob.Type != ObjBlob {
				return corrupt("the file %s (blob %s) is not in the pack", clean(p), e.OID)
			}
			if int64(len(blob.Data)) > w.bytes {
				return errorf(ReasonLimit, "the tree's files hold more than %d bytes", w.lim.MaxBytes)
			}
			w.bytes -= int64(len(blob.Data))
			mode := ModeFile
			if e.Mode == ModeExecutable {
				mode = ModeExecutable
			}
			w.files = append(w.files, File{Path: p, Mode: mode, Data: blob.Data})
		case ModeDir:
			if depth+1 > w.lim.MaxDepth {
				return errorf(ReasonLimit, "the tree nests directories deeper than %d, at %s", w.lim.MaxDepth, clean(p))
			}
			return w.tree(e.OID, p+"/", depth+1)
		case ModeLink:
			return errorf(ReasonRefused, "the tree entry %s is a link (mode 120000); links are not unpacked", clean(p))
		case ModeSubmodule:
			return errorf(ReasonRefused, "the tree entry %s is a submodule (mode 160000); submodules are not fetched", clean(p))
		default:
			return errorf(ReasonRefused, "the tree entry %s has mode %o, which is not a file or a directory", clean(p), e.Mode)
		}
		return nil
	})
}

// Lookup finds the regular file at path (slash-separated) in the tree of
// the commit c, reading only the trees on the way; its blob need not be in
// the store (a "filter blob:none" pack). A link, a submodule or a refused
// name on the way is ReasonRefused; a missing entry ReasonNotFound.
func (s *Store) Lookup(c OID, path string, lim Limits) (TreeEntry, error) {
	lim = lim.withDefaults()
	obj, ok := s.objs[c]
	if !ok || obj.Type != ObjCommit {
		return TreeEntry{}, corrupt("the commit %s is not in the pack", c)
	}
	cur, err := ParseCommit(obj.Data)
	if err != nil {
		return TreeEntry{}, err
	}
	segs := strings.Split(path, "/")
	if len(segs) > lim.MaxDepth+1 {
		return TreeEntry{}, errorf(ReasonLimit, "the path %s nests deeper than %d", clean(path), lim.MaxDepth)
	}
	for i, seg := range segs {
		t, ok := s.objs[cur]
		if !ok || t.Type != ObjTree {
			return TreeEntry{}, corrupt("the tree %s on the way to %s is not in the pack", cur, clean(path))
		}
		var found *TreeEntry
		err := eachEntry(t.Data, func(e TreeEntry) error {
			if e.Name == seg {
				if found != nil {
					return corrupt("a tree holds two entries named %s", clean(seg))
				}
				found = &e
			}
			return nil
		})
		if err != nil {
			return TreeEntry{}, err
		}
		if found == nil {
			return TreeEntry{}, errorf(ReasonNotFound, "the tree has no %s", clean(path))
		}
		if strings.EqualFold(found.Name, ".git") {
			return TreeEntry{}, errorf(ReasonRefused, "the tree entry %s is refused: a .git entry is not unpacked", clean(path))
		}
		last := i == len(segs)-1
		switch {
		case found.Mode == ModeDir && !last:
			cur = found.OID
		case (found.Mode == ModeFile || found.Mode == ModeExecutable || found.Mode == modeGroupFile) && last:
			return *found, nil
		case found.Mode == ModeLink || found.Mode == ModeSubmodule:
			return TreeEntry{}, errorf(ReasonRefused, "the tree entry %s on the way to %s is a link or a submodule", clean(seg), clean(path))
		default:
			return TreeEntry{}, errorf(ReasonNotFound, "%s is not a regular file in the tree", clean(path))
		}
	}
	return TreeEntry{}, errorf(ReasonNotFound, "the tree has no %s", clean(path))
}
