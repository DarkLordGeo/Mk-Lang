// Why: the cache grows with every build; it must stay under a size budget
// without ever removing an entry another process is about to use, and
// without making every command pay a full walk.
//
// What: Stats, GC, MaybeGC and Clean, with the walk, the GC lock (gcLock,
// lock, refresh, unlock), evict, cleanTmp, and gc.stamp (writeStamp,
// readStamp).
//
// How: entries carry last-use marks; GC evicts least recently used first
// under a lock file shared between processes, and never touches an entry
// marked within Grace. gc.stamp records the last run and, when Grace blocked
// it, when a walk can next help, so MaybeGC skips useless walks.

package cache

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrBusy is returned by GC and Clean when another process holds a fresh
// gc.lock. MaybeGC treats it as done; `mk cache clean` reports it.
var ErrBusy = errors.New("cache: another process is collecting")

// lockRefresh is how often a running GC refreshes its lock's mtime, so a
// long collection is never mistaken for a crashed one.
const lockRefresh = time.Minute

// Stats describes the store, or, as GC's result, what GC removed.
//
// From Stats: Entries and Bytes per kind of everything in the store, Total
// their sum in bytes, Oldest the oldest mark, Removed 0.
// From GC: Entries and Bytes per kind of what was removed, Removed the number
// of entries removed, Total the bytes left in the store afterwards, all in
// the sizes GC's walk saw (a file written since the walk is not counted, and
// one found already gone is no longer counted as left).
//
// A raw-file entry and its sidecar count as one entry. The directories of
// Dir (thinlto) and the hint files count under their directory name as kind.
type Stats struct {
	Entries map[Kind]int64 // iterate through SortedKinds, never by range into output
	Bytes   map[Kind]int64
	Total   int64
	Removed int64
	Oldest  time.Time
}

// SortedKinds returns the kinds of st in byte order (determinism).
func (st Stats) SortedKinds() []Kind {
	ks := make([]Kind, 0, len(st.Entries))
	for k := range st.Entries {
		ks = append(ks, k)
	}
	for k := range st.Bytes {
		if _, ok := st.Entries[k]; !ok {
			ks = append(ks, k)
		}
	}
	slices.Sort(ks)
	return ks
}

func (st *Stats) add(kind Kind, size int64) {
	if st.Entries == nil {
		st.Entries = map[Kind]int64{}
		st.Bytes = map[Kind]int64{}
	}
	st.Entries[kind]++
	st.Bytes[kind] += size
}

// gcEntry is one evictable unit: an entry, a raw file with its sidecar
// (sidecar first in paths), a hint file or a file of a Dir. sizes are the
// walk's, one per path; size is their sum.
type gcEntry struct {
	kind  Kind
	paths []string
	sizes []int64
	size  int64
	mark  time.Time
}

// side reports whether e's first path is a sidecar the walk saw.
func (e *gcEntry) side() bool {
	return sideKind(e.kind) && strings.HasSuffix(e.paths[0], ".sum")
}

// sideKind reports whether a file "<x>.sum" under a directory of kind is a
// sidecar: not under hints, lld's thinlto directory or the driver's bin,
// whose files are named by their writers.
func sideKind(kind Kind) bool {
	return kind != hintsDir && kind != ltoDir && kind != BinDir
}

// rawDir reports whether a directory of kind can hold raw-file entries, so
// GC and Clean remove "<file>.sum" after each file they remove there: not
// hints, not the mutable manifest kind, not lld's thinlto directory, not
// bin (PutFile refuses all four).
func rawDir(kind Kind) bool {
	return sideKind(kind) && !Mutable(kind)
}

// walk lists every evictable unit under v1, sorted by (mark, first path),
// oldest first. tmp/, gc.lock and gc.stamp are not units.
func (s *Store) walk() ([]gcEntry, error) {
	s.walks.Add(1)
	tops, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []gcEntry
	for _, top := range tops {
		if !top.IsDir() || top.Name() == tmpDir {
			continue
		}
		kind := Kind(top.Name())
		byBase := map[string]int{} // base path -> index in out, to pair a file with its sidecar
		err := filepath.WalkDir(filepath.Join(s.root, top.Name()), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // removed while walking
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil // removed while walking
			}
			base, side := p, false
			if sideKind(kind) && strings.HasSuffix(p, ".sum") {
				base, side = strings.TrimSuffix(p, ".sum"), true
			}
			i, ok := byBase[base]
			if !ok {
				i = len(out)
				byBase[base] = i
				out = append(out, gcEntry{kind: kind})
			}
			e := &out[i]
			if side {
				e.paths = append([]string{p}, e.paths...)
				e.sizes = append([]int64{fi.Size()}, e.sizes...)
			} else {
				e.paths = append(e.paths, p)
				e.sizes = append(e.sizes, fi.Size())
			}
			e.size += fi.Size()
			if fi.ModTime().After(e.mark) {
				e.mark = fi.ModTime()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(out, func(a, b gcEntry) int {
		if c := a.mark.Compare(b.mark); c != 0 {
			return c
		}
		return strings.Compare(a.paths[0], b.paths[0])
	})
	return out, nil
}

// Stats walks the store: entries and bytes per kind, total, oldest mark.
func (s *Store) Stats() (Stats, error) {
	es, err := s.walk()
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, e := range es {
		st.add(e.kind, e.size)
		st.Total += e.size
	}
	if len(es) > 0 {
		st.Oldest = es[0].mark
	}
	return st, nil
}

// gcLock is a held gc.lock: its path and the token written into it, so it is
// released only if it is still this holder's.
type gcLock struct {
	path    string
	token   string
	touched time.Time
}

// lock creates gc.lock with O_CREATE|O_EXCL, writing pid, start time and a
// random token, and sets its mtime from the store's clock. A lock whose mtime
// is older than LockStale is removed and the create retried once; otherwise
// ErrBusy. Two processes can both break one stale lock and both collect; that
// is benign, because both honor Grace and a delete is idempotent, so the lock
// saves work and never guards correctness.
func (s *Store) lock() (*gcLock, error) {
	p := filepath.Join(s.root, lockFile)
	var r [8]byte
	rand.Read(r[:])
	now := s.now()
	token := fmt.Sprintf("%d %s %s\n", os.Getpid(), now.UTC().Format(time.RFC3339Nano), hex.EncodeToString(r[:]))
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.WriteString(token)
			cerr := f.Close()
			if werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(p)
				return nil, werr
			}
			os.Chtimes(p, now, now)
			return &gcLock{path: p, token: token, touched: now}, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			if merr := os.MkdirAll(s.root, 0o755); merr != nil {
				return nil, merr
			}
			continue
		}
		if !errors.Is(err, fs.ErrExist) && !isTransient(err) {
			return nil, err
		}
		fi, serr := os.Stat(p)
		if serr != nil {
			continue // released meanwhile: try again
		}
		if attempt > 0 || now.Sub(fi.ModTime()) <= LockStale {
			return nil, ErrBusy
		}
		os.Remove(p) // stale: its holder crashed
	}
	return nil, ErrBusy
}

// refresh sets the lock's mtime to now once a minute while GC runs.
func (s *Store) refresh(l *gcLock) {
	now := s.now()
	if now.Sub(l.touched) >= lockRefresh {
		os.Chtimes(l.path, now, now)
		l.touched = now
	}
}

// unlock removes gc.lock if it still holds this holder's token.
func (s *Store) unlock(l *gcLock) {
	b, err := os.ReadFile(l.path)
	if err == nil && string(b) == l.token {
		os.Remove(l.path)
	}
}

// GC evicts least-recently-used entries until the store is at most target
// bytes, under the GC lock. Entries marked within Grace are
// never evicted, so the target may not be reached. tmp/ files older than
// Grace (writers that crashed) are removed. A delete that fails (Windows: the
// file is open in another process) is skipped. It returns what it removed,
// or ErrBusy when another process is collecting.
func (s *Store) GC(target int64) (Stats, error) {
	var st Stats
	if s.opts.ReadOnly {
		return st, nil
	}
	l, err := s.lock()
	if err != nil {
		return st, err
	}
	defer s.unlock(l)
	now := s.now()
	s.cleanTmp(now)
	es, err := s.walk()
	if err != nil {
		return st, err
	}
	hook(s.hooks.afterWalk)
	var total int64
	for _, e := range es {
		total += e.size
	}
	// young re-checks Grace on each file just before its removal: a PutFile
	// may have renamed a fresh file onto a path the walk saw as old.
	young := func(mtime time.Time) bool { return now.Sub(mtime) < Grace }
	// eligible is the earliest time an entry this GC kept for Grace becomes
	// evictable; it reaches the stamp only when the target was not met.
	var eligible time.Time
	for _, e := range es {
		if total <= target {
			break
		}
		s.refresh(l)
		if now.Sub(e.mark) < Grace {
			if at := e.mark.Add(Grace); eligible.IsZero() || at.Before(eligible) {
				eligible = at
			}
			continue
		}
		freed, gone, removed, _ := s.evict(e, young) // a failed delete is skipped
		total -= freed + gone
		if removed {
			st.add(e.kind, freed)
			st.Removed++
		}
	}
	st.Total = total
	// Blocked: the loop saw every entry and still left more than target. Until
	// a kept entry leaves Grace, a walk can free only what failed to delete,
	// which the GCInterval walk retries; so the stamp says when one can help,
	// at most GCInterval ahead.
	var blocked time.Time
	if total > target {
		blocked = now.Add(GCInterval)
		if !eligible.IsZero() && eligible.Before(blocked) {
			blocked = eligible
		}
	}
	s.writeStamp(now, total, blocked)
	return st, nil
}

// evict removes one unit e. A sidecar the walk saw goes first,
// and if it cannot be removed the unit stands whole; then the file.
// After every file it removes (or finds gone) in a directory that can hold
// raw-file entries, it removes the path "<file>.sum" once more, whether or
// not the walk saw a sidecar. young, when not nil, is asked about
// each walked path's mtime just before its removal; a path it calls young is
// kept with the rest of the unit (a PutFile renamed a fresh file onto it
// since the walk). The second sidecar removal is never skipped.
//
// It returns the walk's bytes of the paths it removed (freed) and of those
// it found already gone (gone), whether it removed any walked path, and the
// first error of a removal that failed (Windows: a file another process holds
// open). A sidecar the second removal takes is not counted: the walk did not
// see it, so it is not in the walk's total.
//
// Why no sidecar outlives its file, file D and sidecar S of one key, under
// any interleaving of Store calls in any processes. An actor "can make D
// absent" if it is GC or Clean removing D here, or, to be safe, a PutFile
// whose rename onto D failed (on Windows a stat can see D absent while a
// rename replaces it; a rename that completes leaves D
// standing). Each one, after that, tries to remove S: evict
// unconditionally (the second removal), PutFile if D is missing when it
// stats it (settle). Each PutFile whose rename onto S landed or failed stats
// D after it and removes S if D is missing (settle). Each Store call that
// holds S open (readFile in FilePath, in sidecarStands, in PutFile's
// standing check) stats D after it closes S and removes S if D is missing.
//
// Suppose that at the end S stands and D is absent. Let O be the last time D
// became absent; no rename onto D lands after O, or D would stand. Let S' be
// the sidecar standing at the end, landed at Rs. If Rs is after the attempt
// to remove S made by the actor of O, the PutFile that landed S' stats D
// after Rs, so after O, finds it missing and tries to remove S'. Otherwise
// that actor's attempt was made on S' itself, after O. Either way some
// removal of S' was tried after O, and since S' stands the last such try
// failed. On Linux removal does not fail for an open file; a failure there
// (permission, I/O) is an error the store reports. On Windows it failed on a
// sharing error, so a handle held S' open at that moment; a Store's holder
// stats D after closing it, so after O, finds it missing and tries again,
// which contradicts "last". So only a holder that is not a Store (antivirus,
// an indexer) keeping S' open past removeRetry's third of a second, or a
// process killed between its two steps, can leave S without D, and that is
// a miss: FilePath stats D and compares its length before any hit, and
// removes such a sidecar. A sidecar names a file of its recorded length when
// the file stands, because one key's files are equal (content addressing)
// and a file appears only by rename after its fsync. Removing a
// sidecar only ever turns a hit into a miss, so the extra removals are safe.
func (s *Store) evict(e gcEntry, young func(time.Time) bool) (freed, gone int64, removed bool, err error) {
	side := e.side()
	for i, p := range e.paths {
		if young != nil {
			if fi, serr := os.Lstat(p); serr == nil && young(fi.ModTime()) {
				return freed, gone, removed, nil
			}
		}
		switch rerr := os.Remove(p); {
		case rerr == nil:
			freed += e.sizes[i]
			removed = true
		case errors.Is(rerr, fs.ErrNotExist):
			gone += e.sizes[i]
		default:
			// The sidecar: the unit stands whole. The file: its sidecar is
			// gone, so it is a miss, and a later GC takes the file.
			return freed, gone, removed, rerr
		}
		if i == 0 && side {
			hook(s.hooks.afterSide)
			continue
		}
		if rawDir(e.kind) {
			s.removeLate(p + ".sum")
		}
		if e.kind == BinDir {
			s.removeEmptyParents(p, e.kind)
		}
	}
	return freed, gone, removed, nil
}

// removeEmptyParents removes the directories between the file p, just
// removed, and v1/<kind>, deepest first, while they are empty: a
// bin/<link key>/ directory goes with its executable. A directory that
// still holds something stays. A writer that made the directory just
// before this removed it retries its rename (the driver does).
func (s *Store) removeEmptyParents(p string, kind Kind) {
	top := filepath.Join(s.root, string(kind))
	for d := filepath.Dir(p); len(d) > len(top) && strings.HasPrefix(d, top); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			return
		}
	}
}

// removeLate is evict's second sidecar removal, after the file p names.
func (s *Store) removeLate(sum string) {
	removed, err := removeRetry(sum)
	if removed {
		s.r5.late.Add(1)
	}
	if err != nil && isTransient(err) {
		// Held open past every retry: a Store's holder removes it once it
		// closes it and finds the file missing (see evict).
		s.r5.stuck.Add(1)
	}
}

// cleanTmp removes files in tmp/ older than Grace: writers that crashed
// between write and rename. A torn write was never visible; this reclaims it.
// A directory there is a process's work directory (TmpDir): it is removed
// whole when its newest file, or the directory itself, is older than Grace
// (a process that crashed or was killed before removing it);
// a live one keeps writing and stays.
func (s *Store) cleanTmp(now time.Time) {
	dir := filepath.Join(s.root, tmpDir)
	ds, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, d := range ds {
		p := filepath.Join(dir, d.Name())
		if d.IsDir() {
			if t, ok := newest(p); ok && now.Sub(t) >= Grace {
				os.RemoveAll(p) // best effort: a file held open stays for a later GC
			}
			continue
		}
		fi, err := d.Info()
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) >= Grace {
			os.Remove(p)
		}
	}
}

// newest is the latest mtime of dir and everything under it; a path that
// vanished while walking is skipped. ok is false for a tree that could not
// be read, which is kept.
func newest(dir string) (t time.Time, ok bool) {
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
		return nil
	})
	return t, err == nil && !t.IsZero()
}

// writeStamp records the last GC: gc.stamp's mtime is its time and its
// content the bytes left, in decimal, then, when the GC could not reach its
// target, a space and the time (UnixNano) before which no walk can free more.
func (s *Store) writeStamp(now time.Time, total int64, blocked time.Time) {
	p := filepath.Join(s.root, stampFile)
	text := strconv.FormatInt(total, 10)
	if !blocked.IsZero() {
		text += " " + strconv.FormatInt(blocked.UnixNano(), 10)
	}
	tmp, err := s.writeTmp([]byte(text))
	if err != nil {
		return
	}
	if _, err := s.place(tmp, p, nil, nil); err == nil {
		os.Chtimes(p, now, now)
	}
}

// gcStamp is what gc.stamp records: the last GC's time, the bytes it left,
// and, if it was blocked by Grace, when a walk can next free anything.
type gcStamp struct {
	last    time.Time
	total   int64
	blocked time.Time // zero: the last GC reached its target
}

// readStamp reads gc.stamp. A stamp of one number (the older form, with no
// blocked time) reads as not blocked.
func (s *Store) readStamp() (gcStamp, bool) {
	b, mtime, err := readFile(filepath.Join(s.root, stampFile))
	if err != nil {
		return gcStamp{}, false
	}
	f := strings.Fields(string(b))
	if len(f) < 1 || len(f) > 2 {
		return gcStamp{}, false
	}
	n, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return gcStamp{}, false
	}
	st := gcStamp{last: mtime, total: n}
	if len(f) == 2 {
		ns, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return gcStamp{}, false
		}
		st.blocked = time.Unix(0, ns)
	}
	return st, true
}

// MaybeGC runs GC(Budget*9/10) when the store's last GC stamp is older than
// GCInterval (or missing), or when the size estimate exceeds Budget: the
// total the last GC recorded in gc.stamp plus the bytes this process put.
// Otherwise it does nothing and walks nothing. The driver calls it once,
// after a command's last cache use. Another process collecting is success.
//
// Over the budget, it still does not walk while the last GC was blocked by
// Grace and no entry it kept has left Grace yet: such a walk could
// free nothing, and every command would pay a full walk for up to Grace.
// Bytes put since are younger still, so they do not change that; the cost is
// that a store over its budget stays over it until the earliest kept entry
// ages out, or GCInterval passes, whichever is first.
func (s *Store) MaybeGC() error {
	if s.opts.ReadOnly {
		return nil
	}
	st, ok := s.readStamp()
	if ok {
		now := s.now()
		if now.Sub(st.last) < GCInterval && (st.total+s.put.Load() <= s.opts.Budget || now.Before(st.blocked)) {
			s.carryPut(st)
			return nil
		}
	}
	_, err := s.GC(s.opts.Budget / 10 * 9)
	if errors.Is(err, ErrBusy) {
		return nil
	}
	if err == nil {
		s.put.Store(0) // counted in the stamp now
	}
	return err
}

// Clean removes every entry (mk cache clean), under the GC lock: every kind
// directory, the Dir directories, the hints and the stamp. Writes in progress
// in tmp/ are left to finish (a Put into a removed directory recreates it);
// tmp/ files older than Grace are removed. The store stays open and usable.
//
// Clean removes files as GC does (evict, Grace aside), so a PutFile racing it
// leaves no sidecar without its file, then the directories, deepest first. A
// directory a concurrent write refilled is not an error: Clean walks again,
// at most cleanPasses times in all, and an entry written during the last
// pass may stay. Files another process holds open (Windows) are reported in
// the error.
func (s *Store) Clean() error {
	if s.opts.ReadOnly {
		return nil
	}
	l, err := s.lock()
	if err != nil {
		return err
	}
	defer s.unlock(l)
	s.cleanTmp(s.now())
	var errs []error
	for pass := 0; pass < cleanPasses; pass++ {
		var left bool
		errs, left = s.cleanPass(l)
		if !left {
			break
		}
	}
	return errors.Join(errs...)
}

// cleanPasses bounds Clean's walks: one, and two more if writers refilled a
// directory meanwhile.
const cleanPasses = 3

// cleanPass is one walk of Clean: it removes the top-level files (the stamp)
// but gc.lock, every unit, then every directory but tmp/ that is empty by
// then. It returns the removals that failed and whether anything is left.
func (s *Store) cleanPass(l *gcLock) (errs []error, left bool) {
	tops, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false
		}
		return []error{err}, true
	}
	for _, top := range tops {
		if n := top.Name(); !top.IsDir() && n != lockFile {
			if err := os.Remove(filepath.Join(s.root, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	es, err := s.walk()
	if err != nil {
		return append(errs, err), true
	}
	hook(s.hooks.afterWalk)
	for _, e := range es {
		s.refresh(l)
		if _, _, _, err := s.evict(e, nil); err != nil {
			errs = append(errs, err)
		}
	}
	for _, top := range tops {
		if !top.IsDir() || top.Name() == tmpDir {
			continue
		}
		if !removeDirs(filepath.Join(s.root, top.Name())) {
			left = true
		}
	}
	return errs, left
}

// removeDirs removes dir and every directory under it, deepest first, and
// reports whether dir is gone. A directory that still holds a file stays.
func removeDirs(dir string) bool {
	var dirs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
	_, err := os.Lstat(dir)
	return errors.Is(err, fs.ErrNotExist)
}

// carryPut adds the bytes this Store put to gc.stamp's total when MaybeGC
// does not collect, so the next process's estimate holds them too. Without
// it every process starts from the last GC's total and sees only its own
// puts, and a sequence of commands, each under the budget alone, grows the
// store without bound until GCInterval passes (TestCacheStaysUnderBudget
// in internal/driver). It runs under the GC lock, rewrites the stamp only
// when no GC wrote it since st was read, and keeps the stamp's mtime (the
// last GC's time). A busy lock or a failed write leaves the bytes counted
// in this Store only.
func (s *Store) carryPut(st gcStamp) {
	p := s.put.Load()
	if p <= 0 {
		return
	}
	l, err := s.lock()
	if err != nil {
		return
	}
	defer s.unlock(l)
	cur, ok := s.readStamp()
	if !ok || !cur.last.Equal(st.last) {
		return
	}
	s.writeStamp(cur.last, cur.total+p, cur.blocked)
	s.put.Add(-p)
}
