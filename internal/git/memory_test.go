// Why: fetching a large pack must not cost many times the pack in memory.
// What: TestFetchPeak, the heap peak of FetchTag on a pack of one
// incompressible 64 MiB blob, as a multiple of the pack.
// How: runs only with MK_GIT_MEM=1 (it takes a few hundred MiB) and logs,
// rather than asserts, the multiple.

package git

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchPeak(t *testing.T) {
	if os.Getenv("MK_GIT_MEM") != "1" {
		t.Skip("the memory measurement runs only with MK_GIT_MEM=1")
	}
	blob := make([]byte, 64<<20)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range blob {
		blob[i] = byte(rng.Uint32())
	}
	r := &repoBuilder{}
	b := r.add(ObjBlob, blob)
	root := r.add(ObjTree, treeBytes([]tent{{"100644", "big.bin", b}}))
	commit := r.add(ObjCommit, commitBytes(root, "v1.0.0"))
	pack := writePack(r.objs)
	disc := discoveryReply(true, stdCaps...)
	ls := lsRefsReply(commit.String() + " refs/tags/v1.0.0")
	reply := fetchReply(pack, commit, false)
	packLen := len(pack)
	r.objs, blob = nil, nil
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			w.Write(disc)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		body, _ := io.ReadAll(req.Body)
		if bytes.Contains(body, []byte("command=ls-refs")) {
			w.Write(ls)
		} else {
			w.Write(reply)
		}
	}))
	defer srv.Close()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
		}
	}()
	s, err := (&Client{HTTP: srv.Client()}).FetchTag(srv.URL+"/repo", "v1.0.0")
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	extra := peak.Load() - base
	t.Logf("pack %d MiB; heap peak above the baseline %d MiB, %.2fx the pack (sampled every 2 ms; %d files)", packLen>>20, extra>>20, float64(extra)/float64(packLen), len(s.Files))
}
