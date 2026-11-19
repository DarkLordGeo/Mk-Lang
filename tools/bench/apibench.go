// Why: the httpapi row times servers on a JSON API, and a load generator
// that does not check the answers would time wrong servers too.
//
// What: the sessions of `tools/bench http -row httpapi`
// (tests/bench/app/httpapi/README.md): apiLoad, the route mix; apiSession,
// one connection's requests and checks; apiNote and readHTTP.
//
// How: each connection keeps its own items under its own two tags, so every
// response is predictable from that connection's requests: the status, the
// Location of a created item, and the JSON body byte for byte (after gunzip
// when it is compressed). Its one unknown, a created item's id, is read from
// the response and must grow along the connection.

package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// apiLoad is the route mix: the most items a connection keeps, the
// percentages of each request kind (which sum to 100), a name's length, a
// list's largest limit, and the percentage of requests that accept gzip.
type apiLoad struct {
	Live     int `json:"live"`
	Post     int `json:"post"`
	Get      int `json:"get"`
	List     int `json:"list"`
	Put      int `json:"put"`
	Del      int `json:"delete"`
	Bad      int `json:"bad"` // a POST whose qty is out of range: 422
	NameMin  int `json:"name_min"`
	NameMax  int `json:"name_max"`
	LimitMax int `json:"limit_max"`
	Gzip     int `json:"gzip"`
}

func (l *apiLoad) check() error {
	if l.Live < 1 {
		return errors.New("live must be at least 1")
	}
	for _, p := range []int{l.Post, l.Get, l.List, l.Put, l.Del, l.Bad} {
		if p < 0 {
			return errors.New("the percentages may not be negative")
		}
	}
	if l.Post+l.Get+l.List+l.Put+l.Del+l.Bad != 100 {
		return errors.New("post, get, list, put, delete and bad are percentages summing to 100")
	}
	if l.NameMin < 1 || l.NameMax < l.NameMin || l.NameMax > 64 {
		return errors.New("name_min and name_max: 1 <= min <= max <= 64")
	}
	if l.LimitMax < 1 || l.LimitMax > 100 || l.Gzip < 0 || l.Gzip > 100 {
		return errors.New("limit_max is 1 to 100 and gzip a percentage")
	}
	return nil
}

// apiGzipMin is the row's threshold: a body of at least this many bytes,
// uncompressed, is sent gzip-compressed when the request accepts gzip.
const apiGzipMin = 256

// apiNameBytes is a name's alphabet; a name never starts or ends with the
// space. Nothing in a body needs a JSON escape.
const apiNameBytes = "abcdefghijklmnopqrstuvwxyz0123456789 "

type apiItem struct {
	id        int64
	name, tag string
	qty       int64
}

func (it apiItem) json() string {
	return `{"id":` + strconv.FormatInt(it.id, 10) + `,"name":"` + it.name + `","tag":"` + it.tag + `","qty":` + strconv.FormatInt(it.qty, 10) + `}`
}

func (it apiItem) body() string {
	return `{"name":"` + it.name + `","tag":"` + it.tag + `","qty":` + strconv.FormatInt(it.qty, 10) + `}`
}

// The request kinds.
const (
	apiPost = iota
	apiGet
	apiList
	apiPut
	apiDel
	apiBad
)

// apiSession is one connection's side of the route mix.
type apiSession struct {
	load *apiLoad
	g    splitmix
	tags [2]string
	live []apiItem // by id
	gone int64     // the last id this connection deleted, 0 before any
	last int64     // the largest id this connection was given
	// The request in flight.
	kind   int
	req    []byte
	accept bool
	status int
	want   string  // the body; for a POST, the part after the id
	at     int     // the index into live of the item a request names, -1 for none
	next   apiItem // a POST's or PUT's item
	// What the checked responses held, for -check's line.
	gzipped, notFound, refused, lists int64
}

// newAPISession is connection i's session, drawn from splitmix(seed + i).
func newAPISession(s *serverSpec, i int) session {
	return &apiSession{load: s.API, g: splitmix{s.Seed + uint64(i)*0x9E3779B97F4A7C15},
		tags: [2]string{"c" + strconv.Itoa(i) + "-a", "c" + strconv.Itoa(i) + "-b"}}
}

func (a *apiSession) draw(n int) int { return int(a.g.next() % uint64(n)) }

// pick is a live item's index, or -1 (a deleted or never-made id: 404),
// one chance in len(live)+1.
func (a *apiSession) pick() int {
	j := a.draw(len(a.live) + 1)
	if j == len(a.live) {
		return -1
	}
	return j
}

func (a *apiSession) newItem() apiItem {
	l := a.load
	n := l.NameMin + a.draw(l.NameMax-l.NameMin+1)
	b := make([]byte, n)
	for x := range b {
		alpha := apiNameBytes
		if x == 0 || x == n-1 {
			alpha = apiNameBytes[:len(apiNameBytes)-1]
		}
		b[x] = alpha[a.draw(len(alpha))]
	}
	return apiItem{name: string(b), tag: a.tags[a.draw(2)], qty: int64(a.draw(1_000_000_001))}
}

func (a *apiSession) idPath(at int) string {
	id := a.gone
	if at >= 0 {
		id = a.live[at].id
	}
	return "/items/" + strconv.FormatInt(id, 10)
}

func (a *apiSession) request() []byte {
	l := a.load
	p := a.draw(100)
	a.accept = a.draw(100) < l.Gzip
	var method, path, body string
	switch {
	case p < l.Post:
		a.kind = apiPost
	case p < l.Post+l.Get:
		a.kind = apiGet
	case p < l.Post+l.Get+l.List:
		a.kind = apiList
	case p < l.Post+l.Get+l.List+l.Put:
		a.kind = apiPut
	case p < l.Post+l.Get+l.List+l.Put+l.Del:
		a.kind = apiDel
	default:
		a.kind = apiBad
	}
	full := a.kind == apiPost && len(a.live) >= l.Live
	if full {
		a.kind = apiDel // the oldest goes instead
	}
	a.at = -1
	switch a.kind {
	case apiPost:
		a.next = a.newItem()
		method, path, body = "POST", "/items", a.next.body()
		a.status, a.want = 201, `,"name":"`+a.next.name+`","tag":"`+a.next.tag+`","qty":`+strconv.FormatInt(a.next.qty, 10)+`}`
	case apiBad:
		bad := a.newItem()
		method, path = "POST", "/items"
		body = `{"name":"` + bad.name + `","tag":"` + bad.tag + `","qty":-1}`
		a.status, a.want = 422, `{"message":"an item needs a name of 1 to 64 bytes, a tag of 1 to 32 bytes of a-z, 0-9 and -, and a qty of 0 to 1000000000"}`
	case apiGet:
		a.at = a.pick()
		method, path = "GET", a.idPath(a.at)
		if a.at < 0 {
			a.status, a.want = 404, `{"message":"no such item"}`
		} else {
			a.status, a.want = 200, a.live[a.at].json()
		}
	case apiPut:
		a.at = a.pick()
		a.next = a.newItem()
		method, path, body = "PUT", a.idPath(a.at), a.next.body()
		if a.at < 0 {
			a.status, a.want = 404, `{"message":"no such item"}`
		} else {
			a.next.id = a.live[a.at].id
			a.status, a.want = 200, a.next.json()
		}
	case apiDel:
		if full {
			a.at = 0
		} else {
			a.at = a.pick()
		}
		method, path = "DELETE", a.idPath(a.at)
		if a.at < 0 {
			a.status, a.want = 404, `{"message":"no such item"}`
		} else {
			a.status, a.want = 200, `{"deleted":true}`
		}
	case apiList:
		tag := a.tags[a.draw(2)]
		limit := 1 + a.draw(l.LimitMax)
		method, path = "GET", "/items?tag="+tag+"&limit="+strconv.Itoa(limit)
		var w strings.Builder
		w.WriteString(`{"items":[`)
		total := 0
		for _, it := range a.live {
			if it.tag != tag {
				continue
			}
			if total < limit {
				if total > 0 {
					w.WriteByte(',')
				}
				w.WriteString(it.json())
			}
			total++
		}
		w.WriteString(`],"total":` + strconv.Itoa(total) + `}`)
		a.status, a.want = 200, w.String()
	}
	r := a.req[:0]
	r = append(r, method...)
	r = append(r, ' ')
	r = append(r, path...)
	r = append(r, " HTTP/1.1\r\nHost: 127.0.0.1\r\nUser-Agent: mk-bench\r\n"...)
	if a.accept {
		r = append(r, "Accept-Encoding: gzip\r\n"...)
	}
	if body != "" {
		r = append(r, "Content-Type: application/json\r\nContent-Length: "...)
		r = strconv.AppendInt(r, int64(len(body)), 10)
		r = append(r, "\r\n"...)
	}
	r = append(r, "\r\n"...)
	r = append(r, body...)
	a.req = r
	return r
}

func (a *apiSession) check(br *bufio.Reader) error {
	status, hdr, raw, err := readHTTP(br)
	if err != nil {
		return err
	}
	// what names the request in an error. It is built only on that path,
	// so a sample does not pay for it.
	what := func() string { return strings.SplitN(string(a.req), "\r\n", 2)[0] }
	if status != a.status {
		return fmt.Errorf("%s: status %d, want %d (body %q)", what(), status, a.status, firstLines(string(raw), 1))
	}
	if ct := hdr["content-type"]; ct != "application/json" {
		return fmt.Errorf("%s: Content-Type %q, want application/json", what(), ct)
	}
	body := raw
	gz := hdr["content-encoding"]
	switch gz {
	case "":
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("%s: a gzip body Go's compress/gzip does not read: %w", what(), err)
		}
		if body, err = io.ReadAll(zr); err != nil {
			return fmt.Errorf("%s: a gzip body Go's compress/gzip does not read: %w", what(), err)
		}
	default:
		return fmt.Errorf("%s: Content-Encoding %q", what(), gz)
	}
	if a.kind == apiPost {
		rest, ok := bytes.CutPrefix(body, []byte(`{"id":`))
		k := bytes.IndexByte(rest, ',')
		if !ok || k < 1 {
			return fmt.Errorf("%s: body %q has no id first", what(), body)
		}
		id, err := strconv.ParseInt(string(rest[:k]), 10, 64)
		if err != nil || id <= a.last || strconv.FormatInt(id, 10) != string(rest[:k]) {
			return fmt.Errorf("%s: id %q, after id %d on this connection", what(), rest[:k], a.last)
		}
		if string(rest[k:]) != a.want {
			return fmt.Errorf("%s: body %q, want the id and %q", what(), body, a.want)
		}
		if loc := hdr["location"]; loc != "/items/"+strconv.FormatInt(id, 10) {
			return fmt.Errorf("%s: Location %q for id %d", what(), loc, id)
		}
		a.next.id, a.last = id, id
	} else if string(body) != a.want {
		return fmt.Errorf("%s: body %q, want %q", what(), body, a.want)
	}
	// gzip exactly when the body is long enough and the request accepts it
	if long := len(body) >= apiGzipMin; (gz == "gzip") != (long && a.accept) {
		return fmt.Errorf("%s: Content-Encoding %q for a %d-byte body, accept gzip %v (gzip from %d bytes)", what(), gz, len(body), a.accept, apiGzipMin)
	}
	if gz == "gzip" {
		a.gzipped++
	}
	switch a.status {
	case 404:
		a.notFound++
	case 422:
		a.refused++
	}
	if a.kind == apiList {
		a.lists++
	}
	switch {
	case a.kind == apiPost:
		a.live = append(a.live, a.next)
	case a.kind == apiPut && a.at >= 0:
		a.live[a.at] = a.next
	case a.kind == apiDel && a.at >= 0:
		a.gone = a.live[a.at].id
		a.live = append(a.live[:a.at], a.live[a.at+1:]...)
	}
	return nil
}

// apiNote is -check's line for a run's sessions: what the responses held.
func apiNote(ss []session) string {
	var gz, nf, rf, ls int64
	for _, s := range ss {
		a := s.(*apiSession)
		gz, nf, rf, ls = gz+a.gzipped, nf+a.notFound, rf+a.refused, ls+a.lists
	}
	return fmt.Sprintf(" (%d lists, %d gzip bodies, %d 404s, %d 422s)", ls, gz, nf, rf)
}

// readHTTP reads one HTTP/1.1 response with a Content-Length: its status,
// its headers (names lower-cased; the last of a repeated one) and its
// body. A response without Content-Length, or one that closes the
// connection, is an error.
func readHTTP(br *bufio.Reader) (int, map[string]string, []byte, error) {
	line, err := br.ReadSlice('\n')
	if err != nil {
		return 0, nil, nil, err
	}
	f := strings.Fields(string(line))
	if len(f) < 2 || f[0] != "HTTP/1.1" {
		return 0, nil, nil, fmt.Errorf("status line %q", bytes.TrimSpace(line))
	}
	status, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("status line %q", bytes.TrimSpace(line))
	}
	hdr := map[string]string{}
	for {
		line, err = br.ReadSlice('\n')
		if err != nil {
			return 0, nil, nil, err
		}
		l := bytes.TrimSpace(line)
		if len(l) == 0 {
			break
		}
		k, v, ok := bytes.Cut(l, []byte(":"))
		if !ok {
			return 0, nil, nil, fmt.Errorf("header %q", l)
		}
		hdr[strings.ToLower(string(bytes.TrimSpace(k)))] = string(bytes.TrimSpace(v))
	}
	if strings.Contains(strings.ToLower(hdr["connection"]), "close") {
		return 0, nil, nil, errors.New("the server closes the connection (no keep-alive)")
	}
	n, err := strconv.Atoi(hdr["content-length"])
	if err != nil || n < 0 {
		return 0, nil, nil, fmt.Errorf("Content-Length %q", hdr["content-length"])
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return 0, nil, nil, err
	}
	return status, hdr, body, nil
}
