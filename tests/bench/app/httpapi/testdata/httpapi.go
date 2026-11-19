// httpapi, Go, std: the task of ../README.md, a JSON API over HTTP/1.1 on
// net/http's server, one goroutine per connection (GOMAXPROCS is the row's
// thread count, set by tools/bench), routed by an http.ServeMux with Go
// 1.22's method and wildcard patterns. Items live in a map from id to item
// behind a sync.RWMutex, with the next id beside them. Bodies are checked
// by json.Valid and decoded by json.Unmarshal into a struct of pointers
// (so a missing member is told from a zero one, as Mk's codec does), and
// written by json.Marshal; a body of 256 bytes or more goes out
// gzip-compressed (compress/gzip at its default level) when the request
// accepts gzip, by a gzip.Writer taken from a sync.Pool and Reset for the
// response, as Go servers reuse their compressors. Every response states its Content-Length. Usage: httpapi
// <port>. Serves 127.0.0.1:<port> until killed.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	gzipMin      = 256
	maxName      = 64
	maxTag       = 32
	maxQty       = 1_000_000_000
	maxLimit     = 100
	defaultLimit = 20
	bodyLimit    = 65536
)

type itemIn struct {
	Name *string `json:"name"`
	Tag  *string `json:"tag"`
	Qty  *int64  `json:"qty"`
}

type item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tag  string `json:"tag"`
	Qty  int64  `json:"qty"`
}

type itemList struct {
	Items []item `json:"items"`
	Total int    `json:"total"`
}

type problem struct {
	Message string `json:"message"`
}

type deleted struct {
	Deleted bool `json:"deleted"`
}

type store struct {
	mu    sync.RWMutex
	items map[int64]item
	next  int64
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: httpapi <port>")
		os.Exit(2)
	}
	s := &store{items: map[int64]item{}, next: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", s.create)
	mux.HandleFunc("GET /items", s.list)
	mux.HandleFunc("GET /items/{id}", s.get)
	mux.HandleFunc("PUT /items/{id}", s.put)
	mux.HandleFunc("DELETE /items/{id}", s.delete)
	if err := http.ListenAndServe("127.0.0.1:"+os.Args[1], mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// acceptsGzip reports whether Accept-Encoding names gzip.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(name) == "gzip" {
			return true
		}
	}
	return false
}

// gzipWriters keeps compressors between responses. A gzip.Writer holds
// about 1 MiB of state, so a Go server reuses one through a sync.Pool and
// Resets it per response rather than calling gzip.NewWriter each time:
// std, and the counterpart of the Zig twin's per-connection window
// (both reuse a compressor's state).
var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// reply sends v as JSON with status: gzip-compressed when it is at least
// gzipMin bytes and the request accepts gzip.
func reply(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if len(body) >= gzipMin && acceptsGzip(r) {
		var b bytes.Buffer
		z := gzipWriters.Get().(*gzip.Writer)
		z.Reset(&b)
		z.Write(body)
		z.Close()
		gzipWriters.Put(z)
		body = b.Bytes()
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

func refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	reply(w, r, status, problem{message})
}

func goodTag(tag string) bool {
	if len(tag) == 0 || len(tag) > maxTag {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// readItem is the request's item, or false after a 400 or 422 problem.
func readItem(w http.ResponseWriter, r *http.Request) (item, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyLimit))
	if err != nil {
		refuse(w, r, 413, "the request is larger than the limit")
		return item{}, false
	}
	if !utf8.Valid(body) {
		refuse(w, r, 400, "the body is not UTF-8")
		return item{}, false
	}
	if !json.Valid(body) {
		refuse(w, r, 400, "the body is not JSON")
		return item{}, false
	}
	var in itemIn
	if err := json.Unmarshal(body, &in); err != nil || in.Name == nil || in.Tag == nil || in.Qty == nil {
		refuse(w, r, 422, "an item is an object with a string name and tag and an integer qty")
		return item{}, false
	}
	n := item{Name: *in.Name, Tag: *in.Tag, Qty: *in.Qty}
	if len(n.Name) == 0 || len(n.Name) > maxName || !goodTag(n.Tag) || n.Qty < 0 || n.Qty > maxQty {
		refuse(w, r, 422, "an item needs a name of 1 to 64 bytes, a tag of 1 to 32 bytes of a-z, 0-9 and -, and a qty of 0 to 1000000000")
		return item{}, false
	}
	return n, true
}

// idOf is the route's id, or false after a 400 problem.
func idOf(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		refuse(w, r, 400, "an item id is a number")
		return 0, false
	}
	return id, true
}

func (s *store) create(w http.ResponseWriter, r *http.Request) {
	n, ok := readItem(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	n.ID = s.next
	s.next++
	s.items[n.ID] = n
	s.mu.Unlock()
	w.Header().Set("Location", "/items/"+strconv.FormatInt(n.ID, 10))
	reply(w, r, 201, n)
}

func (s *store) get(w http.ResponseWriter, r *http.Request) {
	id, ok := idOf(w, r)
	if !ok {
		return
	}
	s.mu.RLock()
	it, found := s.items[id]
	s.mu.RUnlock()
	if !found {
		refuse(w, r, 404, "no such item")
		return
	}
	reply(w, r, 200, it)
}

func (s *store) put(w http.ResponseWriter, r *http.Request) {
	id, ok := idOf(w, r)
	if !ok {
		return
	}
	n, ok := readItem(w, r)
	if !ok {
		return
	}
	n.ID = id
	s.mu.Lock()
	_, found := s.items[id]
	if found {
		s.items[id] = n
	}
	s.mu.Unlock()
	if !found {
		refuse(w, r, 404, "no such item")
		return
	}
	reply(w, r, 200, n)
}

func (s *store) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := idOf(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	_, found := s.items[id]
	delete(s.items, id)
	s.mu.Unlock()
	if !found {
		refuse(w, r, 404, "no such item")
		return
	}
	reply(w, r, 200, deleted{true})
}

// queryParam is the raw value of the first name=value pair of a query.
// The API's tags and limits need no escapes, so a value is not decoded.
func queryParam(query, name string) (string, bool) {
	for _, pair := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(pair, "=")
		if k == name {
			return v, true
		}
	}
	return "", false
}

func (s *store) list(w http.ResponseWriter, r *http.Request) {
	tag, _ := queryParam(r.URL.RawQuery, "tag")
	if !goodTag(tag) {
		refuse(w, r, 400, "tag is 1 to 32 bytes of a-z, 0-9 and -")
		return
	}
	limit := int64(defaultLimit)
	if text, ok := queryParam(r.URL.RawQuery, "limit"); ok {
		var err error
		if limit, err = strconv.ParseInt(text, 10, 64); err != nil {
			limit = 0
		}
	}
	if limit < 1 || limit > maxLimit {
		refuse(w, r, 400, "limit is 1 to 100")
		return
	}
	found := make([]item, 0)
	s.mu.RLock()
	for _, it := range s.items {
		if it.Tag == tag {
			found = append(found, it)
		}
	}
	s.mu.RUnlock()
	slices.SortFunc(found, func(a, b item) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	total := len(found)
	if int64(total) > limit {
		found = found[:limit]
	}
	reply(w, r, 200, itemList{found, total})
}
