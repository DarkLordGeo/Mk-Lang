// Why: http.ReadRequest reads untrusted bytes and must agree with its model on any input.
// What: FuzzHttp_ReadRequest, seeded with a sample of ParseDiff's inputs.
// How: the --release ParseDiff driver (std-http_parse_test.go) reads any bytes in
// steps, under any limit, and each line it prints must equal the Go model's.
//
//	go test ./std -run '^$' -fuzz FuzzHttp_ReadRequest -parallel 2   (at most 5 minutes a run)

package std

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func FuzzHttp_ReadRequest(f *testing.F) {
	recs := htInputs()
	for k := 0; k < len(recs); k += 97 {
		r := recs[k]
		if len(r.data) <= 4096 {
			f.Add(r.data, uint8(r.step-1), uint16(r.cap), uint32(r.limit))
		}
	}
	dir, err := os.MkdirTemp("", "mk-http-fuzz")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { os.RemoveAll(dir) })
	var once sync.Once
	var exe string
	f.Fuzz(func(t *testing.T, data []byte, step uint8, cap uint16, limit uint32) {
		once.Do(func() { exe = htBuildParse(t, dir, "release") })
		if exe == "" {
			t.Skip("the driver did not build")
		}
		if len(data) > 4096 {
			data = data[:4096]
		}
		rec := htRecord{data: data, step: 1 + int(step)%64, cap: int(cap) % 257, limit: int(limit % 70000)}
		want := htModelStream(rec)
		r := runNative(t, exe, string(htEncode([]htRecord{rec})))
		if r.code != 0 {
			t.Fatalf("driver exit %d on %q (step %d, cap %d, limit %d)\n%s", r.code, data, rec.step, rec.cap, rec.limit, r.stderr)
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%q (step %d, cap %d, limit %d):\ngot  %q\nwant %q", data, rec.step, rec.cap, rec.limit, got, want)
		}
	})
}
