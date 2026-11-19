// Why: Inflate and Gunzip take untrusted input, so any bytes must give the Go model's verdict.
// What: FuzzSTDCOMPRESS_Inflate: each input goes to one long-running dev-build oracle as an Inflate and a Gunzip record.
// How: a panic, a hang or a differing verdict fails the input.
//
//	go test ./std -run '^$' -fuzz FuzzSTDCOMPRESS_Inflate -fuzztime 5m -parallel 2

package std

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// czServer is a running oracle with its pipes.
type czServer struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errb *bytes.Buffer
}

var czSrv struct {
	sync.Mutex
	s *czServer
}

func czStart(t *testing.T) *czServer {
	exe := czOracleExe(t, "")
	cmd := exec.Command(exe)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &czServer{cmd: cmd, in: in, out: bufio.NewReader(out), errb: &errb}
}

// czAsk sends recs to the running oracle (starting one if needed) and
// returns its answers; a dead oracle fails the test with its stderr.
func czAsk(t *testing.T, recs []czRec) []czRes {
	czSrv.Lock()
	defer czSrv.Unlock()
	if czSrv.s == nil {
		czSrv.s = czStart(t)
	}
	s := czSrv.s
	// The records go in from a goroutine while the answers are read: an
	// input whose answers fill the stdout pipe before all of it is written
	// would otherwise leave both processes waiting on each other.
	werr := make(chan error, 1)
	go func() {
		_, err := s.in.Write(czEncode(recs))
		werr <- err
	}()
	res := make([]czRes, 0, len(recs))
	for range recs {
		r, err := czParse(s.out)
		if err != nil {
			czSrv.s = nil
			s.in.Close()
			s.cmd.Wait()
			t.Fatalf("oracle: %v\nstderr:\n%s", err, s.errb.String())
		}
		res = append(res, r)
	}
	if err := <-werr; err != nil {
		czSrv.s = nil
		s.cmd.Wait()
		t.Fatalf("oracle died: %v\nstderr:\n%s", err, s.errb.String())
	}
	return res
}

func FuzzSTDCOMPRESS_Inflate(f *testing.F) {
	for kind := 0; kind < 5; kind++ {
		b := czInput(kind, 200, 9)
		f.Add(czDeflateGo(f, b, 6))
		f.Add(czDeflateGo(f, b, 1))
		f.Add(czGzipGo(f, b, 6))
	}
	f.Add([]byte{})
	f.Add([]byte{3, 0})
	// Found by the fuzzer: a dynamic block whose code-length code is empty
	// (Go: corrupt at once; Mk said truncated, since its bad entries wanted
	// a bit).
	f.Add([]byte{0x24, 0x30, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		recs := []czRec{
			{op: czInflate, limit: czNoLimit, data: data},
			{op: czGunzip, limit: czNoLimit, data: data},
			{op: czInflate, limit: int64(len(data)), data: data},
		}
		got := czAsk(t, recs)
		for i, r := range recs {
			want := czModel(r)
			if i == 2 && got[i].verdict == "toolarge" && want.verdict != "ok" {
				continue // the output passed the limit before the error
			}
			if !czSame(got[i], want) && !strings.HasPrefix(want.verdict, "error") {
				t.Errorf("op %d limit %d (% x): got %v, want %v", r.op, r.limit, data[:min(len(data), 64)], got[i], want)
			}
		}
	})
}
