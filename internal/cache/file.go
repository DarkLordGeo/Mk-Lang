// Why: objects and import libraries are read by the linker straight from the
// cache, so they cannot carry the frame other entries have; they still need
// a way to tell a complete entry from a torn or dropped one.
//
// What: raw-file entries: PutFile, FilePath, RemoveFile, with the sidecar
// helpers (sidecarPayload, parseSidecar, sidecarStands, removeOrphan) and
// file helpers (moveIn, copyFile, syncAndHash, sameFile).
//
// How: the file holds its exact bytes and a framed sidecar "<file>.sum"
// holds its length and SHA-256. The file is synced and renamed into place
// before the sidecar is written; on use only the key and length are checked,
// not the hash. Every writer, remover and reader rechecks the file after it
// touches the sidecar, so no sidecar outlives its file (the argument is at
// evict in gc.go).

package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"mk/internal/source"
)

// sidecarLen is a sidecar's payload: the file's length (8 bytes, little
// endian), then its SHA-256.
const sidecarLen = 8 + sha256.Size

func sidecarPayload(size int64, sum []byte) []byte {
	p := binary.LittleEndian.AppendUint64(make([]byte, 0, sidecarLen), uint64(size))
	return append(p, sum...)
}

func parseSidecar(p []byte) (size int64, sum []byte, ok bool) {
	if len(p) != sidecarLen {
		return 0, nil, false
	}
	return int64(binary.LittleEndian.Uint64(p[:8])), p[8:], true
}

// PutFile moves (or copies, across volumes) the finished file at path into
// the store as (kind, key), for objects clang wrote. The file at path is gone
// afterwards (in a ReadOnly store it is left where it is). The stored file
// holds the raw bytes with no frame (objects are read by the linker), and a
// sidecar ".sum" holds the frame with the file's length and SHA-256 as its
// payload. The file is synced before it is renamed into place and the sidecar
// is written after, so a sidecar names a complete, durable file: FilePath
// checks only the length, so it cannot detect a torn body. kind is
// content-addressed: an entry whose sidecar already verifies is kept.
//
// After its last rename onto the file's path or the sidecar's, whether the
// rename succeeded or not, PutFile stats the file, and if the file is
// missing (or of another length) it removes the sidecar: a dropped write.
// With GC's and Clean's removal of the sidecar after every file they remove,
// and every reader of a sidecar making the same check once it has closed it,
// this keeps a sidecar from outliving its file under any interleaving
// (the argument is at evict).
func (s *Store) PutFile(kind Kind, key source.Hash, path string) error {
	checkKind(kind)
	if Mutable(kind) {
		source.ICEf(source.Span{}, "cache: PutFile of mutable kind %q", kind)
	}
	if s.opts.ReadOnly {
		return nil
	}
	dst := s.entryPath(kind, key)
	sum := dst + ".sum"
	if mtime, ok := s.sidecarStands(sum, dst, kind, key); ok {
		s.mark(sum, mtime) // as Put does for a standing entry
		os.Remove(path)
		return nil
	}
	tmp, err := s.moveIn(path)
	if err != nil {
		return err
	}
	size, digest, err := syncAndHash(tmp)
	for attempt := 1; attempt < renameAttempts && err != nil && isTransient(err); attempt++ {
		// Windows: another process (a GC removing old tmp files) holds it.
		time.Sleep(time.Millisecond << (2 * (attempt - 1)))
		size, digest, err = syncAndHash(tmp)
	}
	if err != nil && isTransient(err) {
		os.Remove(tmp)
		return s.drop(dst, &droppedError{dst: dst, err: err})
	}
	if err != nil {
		os.Remove(tmp)
		if errors.Is(err, fs.ErrNotExist) { // a GC whose clock runs ahead took it
			return s.drop(dst, &droppedError{dst: dst, err: err})
		}
		return err
	}
	same := func() bool { return sameFile(dst, size, digest) }
	wrote, err := s.place(tmp, dst, nil, same)
	if err != nil {
		// On Windows a stat can see the path absent while a rename replaces
		// the file (this has been observed). No failed rename was seen to
		// leave it absent, but nothing documents that, so a failed one counts
		// as a removal of the file: if the file is missing now, its sidecar
		// goes. That sidecar is not this write's, so it counts as an orphan,
		// not as a second dropped write.
		if _, serr := os.Stat(dst); serr != nil {
			removed, rerr := removeRetry(sum)
			switch {
			case rerr != nil && !isTransient(rerr):
				return rerr
			case rerr != nil:
				s.r5.stuck.Add(1)
			case removed:
				s.r5.orphans.Add(1)
			}
		}
		return s.drop(dst, err)
	}
	if wrote {
		s.put.Add(size)
	}
	hook(s.hooks.afterObj)
	side := encodeFrame(kind, key, sidecarPayload(size, digest))
	stmp, err := s.writeTmp(side)
	if err != nil {
		return err // no rename onto the sidecar's path was tried
	}
	sideStands := func() bool {
		b, _, err := readFile(sum)
		return err == nil && bytes.Equal(b, side)
	}
	wrote, perr := s.place(stmp, sum, func() (string, error) { return s.writeTmp(side) }, sideStands)
	if wrote {
		s.put.Add(int64(len(side)))
	}
	hook(s.hooks.afterSum)
	// A GC may have removed the file after it was placed and before this
	// sidecar landed, its own removal of the sidecar coming too early to see
	// it. The file is checked only now, so a GC that removes it later
	// removes this sidecar after it.
	if serr := s.settle(dst, sum, size); serr != nil {
		return serr
	}
	if perr != nil {
		return s.drop(sum, perr)
	}
	return nil
}

// settle is PutFile's check after its rename onto sum: unless dst is a file
// of the given length, it removes sum, retrying a Windows sharing error, and
// reports a removed sidecar as a dropped write. It returns only an I/O error
// the caller must report.
func (s *Store) settle(dst, sum string, size int64) error {
	fi, err := os.Stat(dst)
	if err == nil && fi.Size() == size {
		return nil
	}
	removed, rerr := removeRetry(sum)
	switch {
	case rerr != nil && !isTransient(rerr):
		return rerr
	case rerr != nil:
		// Held open past every retry by a process that is no Store (a Store's
		// own readers check the file once they close it, see evict): a miss,
		// which the next FilePath of the key removes.
		s.r5.stuck.Add(1)
		return s.drop(sum, &droppedError{dst: sum, err: fmt.Errorf("%w; removing the sidecar failed: %v", errFileGone, rerr)})
	case !removed:
		return nil // no sidecar there
	}
	s.r5.rollbacks.Add(1)
	return s.drop(sum, &droppedError{dst: sum, err: errFileGone})
}

// errFileGone is why a PutFile removed a sidecar: its file was gone (a GC
// removed it, or a rename onto it failed) once the sidecar was in place.
var errFileGone = errors.New("file gone once its sidecar was in place; sidecar rolled back")

// removeRetry removes p, retrying a Windows sharing error (another process
// holds p open) with the backoff of place, and reports whether it removed a
// file. A missing file is success. The sidecar removals in GC, Clean and
// PutFile that keep a sidecar from outliving its file use it.
func removeRetry(p string) (bool, error) {
	var err error
	for attempt := 0; attempt < removeAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Millisecond << (2 * (attempt - 1))) // 1, 4, 16, 64, 256 ms
		}
		err = os.Remove(p)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if !isTransient(err) {
			return false, err
		}
	}
	return false, err
}

// removeAttempts bounds removeRetry: about a third of a second in all, far
// longer than a reader holds a sidecar open (one read of a 121-byte file).
const removeAttempts = 6

// FilePath returns the path of a raw-file entry (objects, import libraries)
// and true if it exists and its sidecar verifies its frame, key and the
// file's length; the linker reads it in place. It marks the entry used: the
// mark is the sidecar's mtime, which GC reads for the pair. A corrupt sidecar,
// or one whose file is missing or of another length, is removed, so the next
// PutFile rewrites the entry.
//
// The file is stated only after the sidecar is closed, and a sidecar whose
// file is missing (or answers with a Windows sharing error) is removed: a
// remover whose removal of the sidecar failed because this read held it open
// leaves the sidecar to this check (see evict).
func (s *Store) FilePath(kind Kind, key source.Hash) (string, bool, error) {
	checkKind(kind)
	dst := s.entryPath(kind, key)
	sum := dst + ".sum"
	b, mtime, err := readFile(sum)
	if err != nil {
		if isMiss(err) {
			return "", false, nil
		}
		return "", false, err
	}
	payload, r := decodeFrame(b, kind, key)
	size, _, ok := parseSidecar(payload)
	if r == frameCorrupt || (r == frameOK && !ok) {
		if !s.opts.ReadOnly {
			os.Remove(sum)
		}
		return "", false, nil
	}
	fi, err := os.Stat(dst)
	if err != nil {
		if isMiss(err) {
			s.removeOrphan(sum)
			return "", false, nil
		}
		return "", false, err
	}
	if r == frameOtherKey {
		return "", false, nil
	}
	if fi.Size() != size {
		if !s.opts.ReadOnly {
			os.Remove(sum)
		}
		return "", false, nil
	}
	s.mark(sum, mtime)
	return dst, true, nil
}

// RemoveFile deletes the raw-file entry (kind, key), sidecar first, as GC
// does, so a reader that races it sees a miss, never a torn entry. A missing
// entry is not an error. The driver calls it when MK_CACHE_VERIFY finds the
// stored file differs from a recomputed one, because PutFile never replaces
// an entry whose sidecar verifies.
func (s *Store) RemoveFile(kind Kind, key source.Hash) error {
	checkKind(kind)
	if s.opts.ReadOnly {
		return nil
	}
	dst := s.entryPath(kind, key)
	if err := os.Remove(dst + ".sum"); err != nil && !isMiss(err) {
		return err
	}
	if err := os.Remove(dst); err != nil && !isMiss(err) {
		return err
	}
	return nil
}

// removeOrphan removes a sidecar whose file a reader found missing after it
// closed the sidecar. One try: if another reader holds it open,
// that reader makes the same check once it closes it.
func (s *Store) removeOrphan(sum string) {
	if s.opts.ReadOnly {
		return
	}
	if os.Remove(sum) == nil {
		s.r5.orphans.Add(1)
	}
}

// sidecarStands reports whether sum verifies for (kind, key) and names a file
// at dst of the recorded length, and returns the sidecar's mtime (the pair's
// mark). Like FilePath, it removes a sidecar whose file is missing.
func (s *Store) sidecarStands(sum, dst string, kind Kind, key source.Hash) (time.Time, bool) {
	b, mtime, err := readFile(sum)
	if err != nil {
		return time.Time{}, false
	}
	payload, r := decodeFrame(b, kind, key)
	if r == frameCorrupt {
		return time.Time{}, false
	}
	size, _, ok := parseSidecar(payload)
	fi, err := os.Stat(dst)
	if err != nil {
		if isMiss(err) {
			s.removeOrphan(sum)
		}
		return time.Time{}, false
	}
	if r != frameOK || !ok || fi.Size() != size {
		return time.Time{}, false
	}
	return mtime, true
}

// moveIn moves the file at path into tmp/, by rename or, across volumes, by
// copy and remove, so the final rename into place stays inside the store.
func (s *Store) moveIn(path string) (string, error) {
	tmp := s.tmpPath()
	err := os.Rename(path, tmp)
	if err == nil {
		return tmp, nil
	}
	if !isCrossDevice(err) {
		return "", err
	}
	if err := copyFile(path, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	os.Remove(path)
	return tmp, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncAndHash reads the file at p through, flushes it to stable storage and
// returns its length and SHA-256.
func syncAndHash(p string) (int64, []byte, error) {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return 0, nil, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, nil, err
	}
	return n, h.Sum(nil), nil
}

// sameFile reports whether the file at p has exactly this length and SHA-256:
// after a rename over it failed, an equal file standing there is the entry.
func sameFile(p string, size int64, digest []byte) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return n == size && bytes.Equal(h.Sum(nil), digest)
}
