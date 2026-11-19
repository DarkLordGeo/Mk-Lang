// Why: the notes service of the examples is the first real program on std.http.
// What: TestHttp_Notes over tests/bench-lanes/std-http/notes, dev and release.
// How: Go's client creates, reads and deletes notes, checks the error statuses, and
// runs 32 clients at once on one shared in-memory store.

package std

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type htNote struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func TestHttp_Notes(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "tests", "bench-lanes", "std-http", "notes", "main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			base := "http://" + htStartServer(t, root, string(src), prof)
			client := &http.Client{Timeout: 10 * time.Second}
			do := func(method, path, body string) (int, string, error) {
				req, err := http.NewRequest(method, base+path, strings.NewReader(body))
				if err != nil {
					return 0, "", err
				}
				res, err := client.Do(req)
				if err != nil {
					return 0, "", err
				}
				defer res.Body.Close()
				b, err := io.ReadAll(res.Body)
				return res.StatusCode, string(b), err
			}
			code, b, err := do("POST", "/notes", `{"title":"first","body":"héllo \"world\"\n"}`)
			if err != nil || code != 201 {
				t.Fatalf("create: %d %q %v", code, b, err)
			}
			var n htNote
			if err := json.Unmarshal([]byte(b), &n); err != nil || n.Title != "first" || n.Body != "héllo \"world\"\n" {
				t.Fatalf("create reply %q: %+v %v", b, n, err)
			}
			code, b, _ = do("GET", fmt.Sprintf("/notes/%d", n.ID), "")
			var g htNote
			if code != 200 || json.Unmarshal([]byte(b), &g) != nil || g != n {
				t.Errorf("read: %d %q", code, b)
			}
			for _, c := range []struct {
				method, path, body string
				code               int
			}{
				{"GET", "/notes/x", "", 400},
				{"GET", "/notes/999999", "", 404},
				{"POST", "/notes", `{"title":1}`, 400},
				{"POST", "/notes", `not json`, 400},
				{"POST", "/notes", `{"title":"` + strings.Repeat("t", 65) + `","body":""}`, 413},
				{"PUT", "/notes", "", 405}, // POST /notes exists
				{"DELETE", fmt.Sprintf("/notes/%d", n.ID), "", 204},
				{"DELETE", fmt.Sprintf("/notes/%d", n.ID), "", 404},
				{"GET", fmt.Sprintf("/notes/%d", n.ID), "", 404},
			} {
				if code, b, err := do(c.method, c.path, c.body); err != nil || code != c.code {
					t.Errorf("%s %s: %d %q %v, want %d", c.method, c.path, code, b, err, c.code)
				}
			}
			// 32 clients at once, each creating, reading back and deleting.
			var wg sync.WaitGroup
			errs := make(chan error, 32)
			for w := range 32 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range 20 {
						title := fmt.Sprintf("w%d-%d", w, i)
						code, b, err := do("POST", "/notes", `{"title":"`+title+`","body":"x"}`)
						var n htNote
						if err != nil || code != 201 || json.Unmarshal([]byte(b), &n) != nil || n.Title != title {
							errs <- fmt.Errorf("create %s: %d %q %v", title, code, b, err)
							return
						}
						code, b, err = do("GET", fmt.Sprintf("/notes/%d", n.ID), "")
						var g htNote
						if err != nil || code != 200 || json.Unmarshal([]byte(b), &g) != nil || g != n {
							errs <- fmt.Errorf("read %s: %d %q %v", title, code, b, err)
							return
						}
						if code, _, err := do("DELETE", fmt.Sprintf("/notes/%d", n.ID), ""); err != nil || code != 204 {
							errs <- fmt.Errorf("delete %s: %d %v", title, code, err)
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}
}
