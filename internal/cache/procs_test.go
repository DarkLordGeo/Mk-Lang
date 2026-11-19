// Why: several mk processes share one cache root, and a GC in one must not
// break a read or write in another.
// What: TestConcurrentProcesses.
// How: many child processes on one root, with a garbage collector deleting
// under them; a read is a miss or the right bytes, never torn, and each
// worker prints its dropped writes by reason.

package cache

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	procWorkers = 4
	procOps     = 1000
	procFuncs   = 64 // func keys shared by every worker
	procObjs    = 16 // obj keys
	procMans    = 8  // manifest keys
	procVariant = 4  // payload variants per manifest key
)

// procPayload is a func entry's payload: a pure function of its key index.
func procPayload(i int) []byte { return testPayload(i, 64+(i*997)%8000) }

// procObj is an object file's bytes.
func procObj(i int) []byte { return testPayload(1000+i, 512+(i*331)%20000) }

// procManifest is variant v of manifest i; any variant is a correct hit.
func procManifest(i, v int) []byte {
	return append([]byte(fmt.Sprintf("m:%d:%d:", i, v)), testPayload(2000+i*10+v, 100*v)...)
}

func validManifest(i int, b []byte) bool {
	for v := 0; v < procVariant; v++ {
		if bytes.Equal(b, procManifest(i, v)) {
			return true
		}
	}
	return false
}

func TestConcurrentProcesses(t *testing.T) {
	if role := os.Getenv("MK_CACHE_CHILD"); role != "" {
		procChild(t, role, os.Getenv("MK_CACHE_ROOT"))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := Open(root, Options{}); err != nil {
		t.Fatal(err)
	}
	start := func(role string) (*exec.Cmd, *bytes.Buffer) {
		cmd := exec.Command(exe, "-test.run=^TestConcurrentProcesses$", "-test.count=1", "-test.timeout=5m")
		cmd.Env = append(os.Environ(), "MK_CACHE_CHILD="+role, "MK_CACHE_ROOT="+root)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, &out
	}
	gc, gcOut := start("gc")
	type proc struct {
		cmd *exec.Cmd
		out *bytes.Buffer
	}
	var workers []proc
	for i := 0; i < procWorkers; i++ {
		c, o := start("worker:" + strconv.Itoa(i))
		workers = append(workers, proc{c, o})
	}
	for i, w := range workers {
		if err := w.cmd.Wait(); err != nil {
			t.Errorf("worker %d: %v\n%s", i, err, w.out)
		}
	}
	os.WriteFile(filepath.Join(root, "stop"), nil, 0o644)
	if err := gc.Wait(); err != nil {
		t.Errorf("gc: %v\n%s", err, gcOut)
	}
	all := gcOut.String()
	for _, w := range workers {
		all += w.out.String()
	}
	for _, line := range strings.Split(all, "\n") {
		if strings.Contains(line, "procstat") {
			t.Log(strings.TrimSpace(line))
		}
	}
	// Every final entry verifies, and every hit is the right payload.
	s, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	verifyAll(t, s)
	if n := countFiles(t, filepath.Join(s.Root(), tmpDir)); n != 0 {
		t.Errorf("%d files left in tmp/", n)
	}
}

// verifyAll checks every file of the store: framed entries verify for the
// key in their own frame, sidecars name a file of their length, and every
// hit through the API is the payload of its key.
func verifyAll(t *testing.T, s *Store) {
	t.Helper()
	for _, kind := range []Kind{KindFunc, KindManifest} {
		filepath.WalkDir(filepath.Join(s.Root(), string(kind)), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, _ := os.ReadFile(p)
			if len(b) < frameFixed+len(kind) {
				t.Errorf("short entry %s", p)
				return nil
			}
			var key [32]byte
			copy(key[:], b[6+len(kind):])
			if _, r := decodeFrame(b, kind, key); r != frameOK {
				t.Errorf("entry %s does not verify", p)
			}
			return nil
		})
	}
	// Every sidecar verifies for the key in its own frame and names a file of
	// its recorded length.
	filepath.WalkDir(filepath.Join(s.Root(), string(KindObj)), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".sum" {
			return nil
		}
		b, _ := os.ReadFile(p)
		if len(b) < frameFixed+len(KindObj) {
			t.Errorf("short sidecar %s", p)
			return nil
		}
		var key [32]byte
		copy(key[:], b[6+len(KindObj):])
		payload, r := decodeFrame(b, KindObj, key)
		size, _, ok := parseSidecar(payload)
		if r != frameOK || !ok {
			t.Errorf("sidecar %s does not verify", p)
			return nil
		}
		if fi, err := os.Stat(p[:len(p)-len(".sum")]); err != nil || fi.Size() != size {
			t.Errorf("sidecar %s names a missing file or one of the wrong length: %v", p, err)
		}
		return nil
	})
	for i := 0; i < procFuncs; i++ {
		if b, ok, err := s.Get(KindFunc, testKey(i)); err != nil || (ok && !bytes.Equal(b, procPayload(i))) {
			t.Errorf("func %d: wrong payload or %v", i, err)
		}
	}
	for i := 0; i < procMans; i++ {
		if b, ok, err := s.Get(KindManifest, testKey(5000+i)); err != nil || (ok && !validManifest(i, b)) {
			t.Errorf("manifest %d: wrong payload or %v", i, err)
		}
	}
	for i := 0; i < procObjs; i++ {
		p, ok, err := s.FilePath(KindObj, testKey(3000+i))
		if err != nil {
			t.Errorf("obj %d: %v", i, err)
		}
		if ok {
			if b, _ := os.ReadFile(p); !bytes.Equal(b, procObj(i)) {
				t.Errorf("obj %d: wrong bytes", i)
			}
		}
	}
}

// procChild runs one child role: "worker:<n>" does procOps mixed operations;
// "gc" collects in a loop, with a clock three hours ahead so that every entry
// is past Grace and is evicted under the workers, until the stop file exists.
func procChild(t *testing.T, role, root string) {
	if role == "gc" {
		ahead := func() time.Time { return time.Now().Add(Grace + time.Hour) }
		s, err := Open(root, Options{Now: ahead})
		if err != nil {
			t.Fatal(err)
		}
		runs, removed := 0, int64(0)
		for {
			st, err := s.GC(0)
			if err != nil && !errors.Is(err, ErrBusy) {
				t.Fatalf("GC: %v", err)
			}
			runs++
			removed += st.Removed
			time.Sleep(15 * time.Millisecond) // let entries and in-flight PutFiles live long enough to hit
			if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
				break
			}
		}
		fmt.Printf("procstat gc: %d runs, %d entries removed; sidecars removed after their file %d, removals stuck %d\n", runs, removed, s.r5.late.Load(), s.r5.stuck.Load())
		return
	}
	n, err := strconv.Atoi(strings.TrimPrefix(role, "worker:"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var dropped []string
	s, err := Open(root, Options{Logf: func(f string, a ...any) {
		mu.Lock()
		dropped = append(dropped, fmt.Sprintf(f, a...))
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src"+strconv.Itoa(n))
	os.MkdirAll(src, 0o755)
	r := rand.New(rand.NewPCG(uint64(n), 7))
	hits, misses, objHits := 0, 0, 0
	for op := 0; op < procOps; op++ {
		switch k := r.IntN(10); {
		case k < 3:
			i := r.IntN(procFuncs)
			if err := s.Put(KindFunc, testKey(i), procPayload(i)); err != nil {
				t.Fatalf("op %d Put func %d: %v", op, i, err)
			}
		case k < 6:
			i := r.IntN(procFuncs)
			b, ok, err := s.Get(KindFunc, testKey(i))
			if err != nil {
				t.Fatalf("op %d Get func %d: %v", op, i, err)
			}
			if ok && !bytes.Equal(b, procPayload(i)) {
				t.Fatalf("op %d Get func %d: wrong payload", op, i)
			}
			if ok {
				hits++
			} else {
				misses++
			}
		case k < 7:
			i := r.IntN(procObjs)
			p := filepath.Join(src, fmt.Sprintf("%d-%d.o", i, op))
			if err := os.WriteFile(p, procObj(i), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := s.PutFile(KindObj, testKey(3000+i), p); err != nil {
				t.Fatalf("op %d PutFile obj %d: %v", op, i, err)
			}
		case k < 8:
			i := r.IntN(procObjs)
			p, ok, err := s.FilePath(KindObj, testKey(3000+i))
			if err != nil {
				t.Fatalf("op %d FilePath obj %d: %v", op, i, err)
			}
			if ok {
				objHits++
				b, err := os.ReadFile(p)
				if err == nil && !bytes.Equal(b, procObj(i)) {
					t.Fatalf("op %d obj %d: wrong bytes", op, i)
				}
				if err != nil && !isMiss(err) {
					t.Fatalf("op %d read obj %d: %v", op, i, err)
				}
			}
		case k < 9:
			i := r.IntN(procMans)
			if err := s.Put(KindManifest, testKey(5000+i), procManifest(i, r.IntN(procVariant))); err != nil {
				t.Fatalf("op %d Put manifest %d: %v", op, i, err)
			}
		default:
			i := r.IntN(procMans)
			b, ok, err := s.Get(KindManifest, testKey(5000+i))
			if err != nil {
				t.Fatalf("op %d Get manifest %d: %v", op, i, err)
			}
			if ok && !validManifest(i, b) {
				t.Fatalf("op %d Get manifest %d: torn or wrong", op, i)
			}
		}
	}
	fmt.Printf("procstat worker %d: %d func hits, %d misses, %d obj hits, %d dropped writes\n", n, hits, misses, objHits, len(dropped))
	why := map[string]int{}
	for _, d := range dropped {
		switch {
		case strings.Contains(d, errFileGone.Error()):
			why["rollback"]++ // PutFile removed its sidecar: the file was gone
		case strings.Contains(d, ".sum"):
			why["sidecar"]++
		case strings.Contains(d, string(filepath.Separator)+"obj"+string(filepath.Separator)):
			why["obj"]++
		default:
			why["framed"]++
		}
	}
	fmt.Printf("procstat worker %d: dropped obj %d, sidecar %d, rollback %d, framed %d; orphan sidecars removed %d, removals stuck %d\n", n, why["obj"], why["sidecar"], why["rollback"], why["framed"], s.r5.orphans.Load(), s.r5.stuck.Load())
}
