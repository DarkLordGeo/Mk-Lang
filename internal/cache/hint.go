// Why: hashing every source file on every build is a large cost of a warm
// no-op build; a file whose stat is unchanged can reuse its last hash, as
// Git's index does, provided an edit can never hide behind an unchanged stat.
//
// What: Hints (LoadHints, Lookup, Record, Save), the per-package stat hint
// for source files, and Store.FileHash, a memo of the compiler executable's
// hash; hintRec is one record.
//
// How: a record holds size, mtime, a change identity (ctime and inode on
// Linux, the creation time on Windows) and when the stat was taken. Stat data
// comes from one ReadDir per directory. A record whose mtime is within
// racyWindow of its stat time is racy and never hits. Hints are never part of
// a cache key.

package cache

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"mk/internal/source"
)

// hintKind is the frame kind of hint files; it is the hints directory, so it
// is not an entry kind (checkKind refuses it).
const hintKind Kind = hintsDir

// racyWindow: a record is racy when the file's mtime is not strictly older
// than the time its stat was taken minus this (the rule Git uses for its
// index, anchored where Git anchors it: at the stat, not at the write).
const racyWindow = 2 * time.Second

// maxExeMemo bounds the FileHash memo; the records written longest ago go.
const maxExeMemo = 64

// hintRec is one file's stat record and content hash.
type hintRec struct {
	size    int64
	mtime   int64     // UnixNano, full resolution
	ident   [2]uint64 // change identity: Linux ctime and inode, Windows creation time
	hash    source.Hash
	written int64 // UnixNano by the store's clock: when the stat was taken (no later)
}

// matches reports whether fi has rec's stat record and rec is not racy. A
// stat of anything but a regular file never matches: DirEntry.Info
// is an Lstat, so for a symbolic link every field, the change identity
// included, is the link's, and an edit of its target changes none of them.
func (r hintRec) matches(fi os.FileInfo) bool {
	if !fi.Mode().IsRegular() {
		return false
	}
	if fi.Size() != r.size || fi.ModTime().UnixNano() != r.mtime || changeIdentity(fi) != r.ident {
		return false
	}
	return time.Unix(0, r.mtime).Before(time.Unix(0, r.written).Add(-racyWindow))
}

// newRec makes the record of fi and content. written is statAt, the time the
// stat was taken, clamped to now so a caller's clock running ahead
// cannot make a record look older than it is.
func newRec(fi os.FileInfo, content source.Hash, statAt, now time.Time) hintRec {
	if statAt.After(now) {
		statAt = now
	}
	written := int64(math.MinInt64) // a zero statAt: racy for every mtime
	if !statAt.IsZero() {
		written = statAt.UnixNano()
	}
	return hintRec{size: fi.Size(), mtime: fi.ModTime().UnixNano(), ident: changeIdentity(fi), hash: content, written: written}
}

// Hints is the local stat hint for source files (the size and mtime
// hint): it maps a file to its content hash when the file's
// stat record is unchanged and not racy. It is never part of a key. Safe for
// concurrent use.
type Hints struct {
	s     *Store
	path  string
	key   source.Hash
	mu    sync.Mutex
	recs  map[string]hintRec
	dirty bool
}

// LoadHints reads the hint file for a package root (keyed by the hash of the
// root's absolute path). A missing or corrupt hint file is an empty Hints.
func (s *Store) LoadHints(pkgRoot string) *Hints {
	abs, err := filepath.Abs(pkgRoot)
	if err != nil {
		abs = pkgRoot
	}
	// v2: written is the stat time; a v1 file, whose written
	// is later than its stat, loads as another key and so as empty.
	key := source.HashString("mk.hintroot.v2", abs)
	name := key.Trunc128().String()
	h := &Hints{s: s, path: filepath.Join(s.root, hintsDir, name), key: key}
	h.recs = s.loadRecs(h.path, key)
	return h
}

// Lookup returns the recorded content hash of the file at path (the file's
// package-relative path, as Record was given it) if its stat record equals
// the recorded one and the record is not racy. fi comes from one os.ReadDir
// per directory (DirEntry.Info), so no source file is opened for a hint
// check; Lookup never calls os.Stat or opens path. A stat of anything but a
// regular file never hits: the caller reads and hashes a symbolic
// link's target every build.
func (h *Hints) Lookup(path string, fi os.FileInfo) (source.Hash, bool) {
	h.mu.Lock()
	r, ok := h.recs[path]
	h.mu.Unlock()
	if !ok || !r.matches(fi) {
		return source.Hash{}, false
	}
	return r.hash, true
}

// Record notes path's stat record and content hash; Save writes the file
// atomically. Recording a racy stat is allowed; Lookup will distrust it.
//
// fi is the stat taken before the content was read, and statAt is a time by
// the store's clock (Store.Now) read no later than that stat: the driver
// reads it once before the os.ReadDir whose DirEntry.Info gives fi. The racy
// rule is anchored at statAt, not at the call: an edit after the
// stat either changes the stat, or leaves the mtime within the clock's tick of
// statAt, so with a tick of at most 2 seconds (FAT) the record is racy and the
// next Lookup misses, however late Record runs. A zero statAt never hits. The
// store clock and the file system's clock are assumed to agree within the
// window; a network share whose server clock runs behind breaks that, as it
// breaks Git's index. A stat of anything but a regular file (a symbolic link:
// DirEntry.Info does not follow it) is not recorded, and drops any record
// path had.
func (h *Hints) Record(path string, fi os.FileInfo, statAt time.Time, content source.Hash) {
	if !fi.Mode().IsRegular() {
		h.mu.Lock()
		if _, ok := h.recs[path]; ok {
			delete(h.recs, path)
			h.dirty = true
		}
		h.mu.Unlock()
		return
	}
	r := newRec(fi, content, statAt, h.s.now())
	h.mu.Lock()
	if old, ok := h.recs[path]; !ok || old != r {
		h.recs[path] = r
		h.dirty = true
	}
	h.mu.Unlock()
}

// Save writes the hint file atomically if anything was recorded since the
// load (a no-op in a ReadOnly store). A write that loses a race with another
// process's is dropped: the cost is a rehash.
func (h *Hints) Save() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.dirty || h.s.opts.ReadOnly {
		return nil
	}
	if err := h.s.saveRecs(h.path, h.key, h.recs); err != nil {
		return err
	}
	h.dirty = false
	return nil
}

// FileHash returns the SHA-256 (source.HashBytes under "mk.exe.v1") of a file
// outside any package, memoized in hints/exe by (absolute path, size, mtime,
// change identity) so a warm process does not rehash it. The
// driver uses it for the compiler executable. A racy record
// is rehashed.
func (s *Store) FileHash(path string) (source.Hash, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return source.Hash{}, err
	}
	statAt := s.now() // before the stat: the racy rule's anchor
	fi, err := os.Stat(abs)
	if err != nil {
		return source.Hash{}, err
	}
	memo := filepath.Join(s.root, hintsDir, "exe")
	key := source.HashString("mk.hintexe.v2", "exe")
	recs := s.loadRecs(memo, key)
	if r, ok := recs[abs]; ok && r.matches(fi) {
		return r.hash, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return source.Hash{}, err
	}
	s.reads.Add(1)
	h := source.HashBytes("mk.exe.v1", b)
	if s.opts.ReadOnly {
		return h, nil
	}
	recs[abs] = newRec(fi, h, statAt, s.now())
	for len(recs) > maxExeMemo {
		oldest := ""
		for p, r := range recs {
			if oldest == "" || r.written < recs[oldest].written || (r.written == recs[oldest].written && p < oldest) {
				oldest = p
			}
		}
		delete(recs, oldest)
	}
	if err := s.saveRecs(memo, key, recs); err != nil {
		s.logf("cache: hint memo not saved: %v", err) // the cost is a rehash
	}
	return h, nil
}

// loadRecs reads a hint file; a missing or corrupt one is empty.
func (s *Store) loadRecs(p string, key source.Hash) map[string]hintRec {
	recs := map[string]hintRec{}
	b, _, err := readFile(p)
	if err != nil {
		return recs
	}
	payload, r := decodeFrame(b, hintKind, key)
	if r != frameOK {
		return recs
	}
	d := hintDecoder{b: payload, ok: true}
	n := d.uvarint()
	for i := uint64(0); i < n && d.ok; i++ {
		path := d.str()
		var rec hintRec
		rec.size = d.varint()
		rec.mtime = d.varint()
		rec.ident[0] = d.uvarint()
		rec.ident[1] = d.uvarint()
		copy(rec.hash[:], d.bytes(32))
		rec.written = d.varint()
		if d.ok {
			recs[path] = rec
		}
	}
	if !d.ok || len(d.b) != 0 {
		return map[string]hintRec{}
	}
	return recs
}

// saveRecs writes recs, sorted by path, as a framed file at p, replacing it
// by rename.
func (s *Store) saveRecs(p string, key source.Hash, recs map[string]hintRec) error {
	paths := make([]string, 0, len(recs))
	for path := range recs {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	var out []byte
	out = binary.AppendUvarint(out, uint64(len(paths)))
	for _, path := range paths {
		r := recs[path]
		out = binary.AppendUvarint(out, uint64(len(path)))
		out = append(out, path...)
		out = binary.AppendVarint(out, r.size)
		out = binary.AppendVarint(out, r.mtime)
		out = binary.AppendUvarint(out, r.ident[0])
		out = binary.AppendUvarint(out, r.ident[1])
		out = append(out, r.hash[:]...)
		out = binary.AppendVarint(out, r.written)
	}
	data := encodeFrame(hintKind, key, out)
	tmp, err := s.writeTmp(data)
	if err != nil {
		return err
	}
	_, err = s.place(tmp, p, func() (string, error) { return s.writeTmp(data) }, nil)
	return s.drop(p, err)
}

// hintDecoder reads a hint payload; any short read clears ok.
type hintDecoder struct {
	b  []byte
	ok bool
}

func (d *hintDecoder) uvarint() uint64 {
	if !d.ok {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.ok = false
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *hintDecoder) varint() int64 {
	if !d.ok {
		return 0
	}
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.ok = false
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *hintDecoder) bytes(n int) []byte {
	if !d.ok || len(d.b) < n {
		d.ok = false
		return nil
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func (d *hintDecoder) str() string {
	n := d.uvarint()
	if n > uint64(len(d.b)) {
		d.ok = false
		return ""
	}
	return string(d.bytes(int(n)))
}
