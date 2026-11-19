// Why: reading one package's pkg.mk should not download the whole repository.
// What: TestFetchFileFiltered, over fixtures in testdata/synthetic-filter
// that filterFixtures writes.
// How: FetchFile fetches the tag's commit with "filter blob:none", finds
// pkg.mk's blob id in its trees and fetches that blob alone; a server with no
// filter feature gets one whole fetch.

package git

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const filterDir = "testdata/synthetic-filter"

// filterFixtures are the manifest-read sets: "filter" (a server with the
// filter feature: fetch-filtered.pkt holds no blob, fetch-blob.pkt the
// manifest's blob), "blob-refused" (the blob want is refused with ERR,
// and FetchFile falls back to one whole fetch, fetch.pkt) and "no-filter"
// (fetch.pkt holds everything).
func filterFixtures() map[string]map[string][]byte {
	manifest := []byte("const pkg = Package(name: \"demo\", version: \"1.0.0\")\n")
	other := []byte(strings.Repeat("big file nobody needs\n", 50))
	manOID := HashObject(ObjBlob, manifest)
	otherOID := HashObject(ObjBlob, other)
	src := treeBytes([]tent{{"100644", "big.mk", otherOID}})
	srcOID := HashObject(ObjTree, src)
	root := treeBytes([]tent{{"100644", "pkg.mk", manOID}, {"40000", "src", srcOID}})
	rootOID := HashObject(ObjTree, root)
	commit := commitBytes(rootOID, "v1.0.0")
	commitOID := HashObject(ObjCommit, commit)
	tag := tagBytes(commitOID, "v1.0.0")
	tagOID := HashObject(ObjTag, tag)
	ls := lsRefsReply(tagOID.String() + " refs/tags/v1.0.0 peeled:" + commitOID.String())
	trees := []pobj{{typ: ObjTree, data: src}, {typ: ObjTree, data: root}, {typ: ObjCommit, data: commit}, {typ: ObjTag, data: tag}}
	all := append([]pobj{{typ: ObjBlob, data: manifest}, {typ: ObjBlob, data: other}}, trees...)
	sum := sha256.Sum256(manifest)
	expect := func(filtered bool) []byte {
		return []byte("blob " + manOID.String() + "\nsha256 " + hex.EncodeToString(sum[:]) + "\nfiltered " + map[bool]string{true: "true", false: "false"}[filtered] + "\n")
	}
	return map[string]map[string][]byte{
		"filter": {
			"discovery.pkt":      discoveryReply(true, stdCaps...),
			"ls-refs.pkt":        ls,
			"fetch-filtered.pkt": fetchReply(writePack(trees), commitOID, false),
			"fetch-blob.pkt":     fetchReply(writePack([]pobj{{typ: ObjBlob, data: manifest}}), OID{}, false),
			"expect.txt":         expect(true),
		},
		"blob-refused": {
			// The server filters but refuses a want that is not a commit
			// (no uploadpack.allowAnySHA1InWant): an ERR line.
			"discovery.pkt":      discoveryReply(true, stdCaps...),
			"ls-refs.pkt":        ls,
			"fetch-filtered.pkt": fetchReply(writePack(trees), commitOID, false),
			"fetch-blob.pkt":     errReply("upload-pack: not our ref " + manOID.String()),
			"fetch.pkt":          fetchReply(writePack(all), commitOID, false),
			"expect.txt":         expect(false),
		},
		"no-filter": {
			"discovery.pkt": discoveryReply(true, "agent=git/2.45.0", "ls-refs", "fetch=shallow", "object-format=sha1"),
			"ls-refs.pkt":   ls,
			"fetch.pkt":     fetchReply(writePack(all), commitOID, false),
			"expect.txt":    expect(false),
		},
	}
}

func errReply(text string) []byte {
	w := &PktWriter{}
	w.Line("ERR " + text)
	return w.Bytes()
}

// filterServer serves a filter set and records the fetch bodies.
func filterServer(t *testing.T, dir string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var fetches []string
	read := func(name string) []byte {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			w.Write(read("discovery.pkt"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		switch {
		case bytes.Contains(body, []byte("command=ls-refs\n")):
			w.Write(read("ls-refs.pkt"))
			return
		case bytes.Contains(body, []byte("filter blob:none\n")):
			w.Write(read("fetch-filtered.pkt"))
		case !bytes.Contains(body, []byte("deepen")) && read("fetch-blob.pkt") != nil:
			w.Write(read("fetch-blob.pkt"))
		default:
			w.Write(read("fetch.pkt"))
		}
		mu.Lock()
		fetches = append(fetches, string(body))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, &fetches
}

func TestFetchFileFiltered(t *testing.T) {
	for _, set := range []string{"filter", "blob-refused", "no-filter"} {
		t.Run(set, func(t *testing.T) {
			dir := filepath.Join(filterDir, set)
			srv, fetches := filterServer(t, dir)
			f, err := (&Client{HTTP: srv.Client()}).FetchFile(srv.URL+"/repo", "v1.0.0", "pkg.mk")
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(f.Data)
			got := "blob " + f.Blob.String() + "\nsha256 " + hex.EncodeToString(sum[:]) + "\nfiltered " + map[bool]string{true: "true", false: "false"}[f.Filtered] + "\n"
			want, _ := os.ReadFile(filepath.Join(dir, "expect.txt"))
			if got != string(want) {
				t.Errorf("got\n%swant\n%s", got, want)
			}
			if set == "blob-refused" {
				if len(*fetches) != 3 || !strings.Contains((*fetches)[0], "filter blob:none") || strings.Contains((*fetches)[1], "deepen") ||
					!strings.Contains((*fetches)[2], "deepen 1") || strings.Contains((*fetches)[2], "filter") {
					t.Errorf("fetch requests %q", *fetches)
				}
			} else if set == "filter" {
				if len(*fetches) != 2 || !strings.Contains((*fetches)[0], "filter blob:none\n") || !strings.Contains((*fetches)[0], "deepen 1\n") ||
					!strings.Contains((*fetches)[1], "want "+f.Blob.String()+"\n") || strings.Contains((*fetches)[1], "deepen") || strings.Contains((*fetches)[1], "filter") {
					t.Errorf("fetch requests %q", *fetches)
				}
			} else if len(*fetches) != 1 || strings.Contains((*fetches)[0], "filter") {
				t.Errorf("fetch requests %q", *fetches)
			}
		})
	}
	srv, _ := filterServer(t, filepath.Join(filterDir, "filter"))
	c := &Client{HTTP: srv.Client()}
	if _, err := c.FetchFile(srv.URL+"/repo", "v1.0.0", "missing.mk"); !isReason(err, ReasonNotFound) {
		t.Errorf("a missing file: %v", err)
	}
	if _, err := c.FetchFile(srv.URL+"/repo", "v1.0.0", "src"); !isReason(err, ReasonNotFound) {
		t.Errorf("a directory: %v", err)
	}
}
