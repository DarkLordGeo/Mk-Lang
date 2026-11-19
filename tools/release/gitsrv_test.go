// Why: the seed's tests need a git server for framework tags without the
// network, and a tool cannot import internal/driver's test server.
//
// What: gitObj, gitRepo and the smart HTTP server the seed tests fetch from.
//
// How: a copy of internal/driver/gitsrv_test.go speaking protocol version 2
// (discovery, ls-refs over refs/tags/v, fetch of one commit, whole or with
// "filter blob:none"). Every repository is served under
// https://example.com/<path>, which httptest's certificate covers; the
// client dials the server whatever the host.

package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"mk/internal/git"
)

// gitObj is one object of a synthetic repository.
type gitObj struct {
	typ  git.ObjectType
	data []byte
}

// gitRepo is one repository: its objects and its version tags.
type gitRepo struct {
	objs map[git.OID]gitObj
	tags map[string]git.OID // "v1.0.0" -> commit
}

// gitServer serves repositories by path ("someone/toml").
type gitServer struct {
	t      testing.TB
	srv    *httptest.Server
	mu     sync.Mutex
	repos  map[string]*gitRepo
	noFilt bool // advertise no filter capability
	// fetches counts fetch commands, filtered ones apart.
	fetches, filtered int
}

func newGitServer(t testing.TB) *gitServer {
	gs := &gitServer{t: t, repos: map[string]*gitRepo{}}
	gs.srv = httptest.NewTLSServer(http.HandlerFunc(gs.serve))
	t.Cleanup(gs.srv.Close)
	return gs
}

// client is an HTTP client that reaches the server for any host.
func (gs *gitServer) client() *http.Client {
	c := gs.srv.Client()
	tr := c.Transport.(*http.Transport).Clone()
	addr := gs.srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	c.Transport = tr
	return c
}

// source is the Dep.source of repository path.
func (gs *gitServer) source(path string) string { return "example.com/" + path }

func (r *gitRepo) add(t git.ObjectType, data []byte) git.OID {
	id := git.HashObject(t, data)
	r.objs[id] = gitObj{t, data}
	return id
}

// tag adds a commit of files (slash paths) as tag v<version> of the
// repository path.
func (gs *gitServer) tag(path, version string, files map[string]string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	r := gs.repos[path]
	if r == nil {
		r = &gitRepo{objs: map[git.OID]gitObj{}, tags: map[string]git.OID{}}
		gs.repos[path] = r
	}
	root := r.tree(files)
	commit := r.add(git.ObjCommit, []byte("tree "+root.String()+
		"\nauthor A <a@example.com> 1700000000 +0000\ncommitter A <a@example.com> 1700000000 +0000\n\nv"+version+"\n"))
	r.tags["v"+version] = commit
}

// tree writes the tree of files and returns its id.
func (r *gitRepo) tree(files map[string]string) git.OID {
	type ent struct {
		mode, name string
		oid        git.OID
	}
	dirs := map[string]map[string]string{}
	var ents []ent
	for p, text := range files {
		if dir, rest, ok := strings.Cut(p, "/"); ok {
			if dirs[dir] == nil {
				dirs[dir] = map[string]string{}
			}
			dirs[dir][rest] = text
			continue
		}
		ents = append(ents, ent{"100644", p, r.add(git.ObjBlob, []byte(text))})
	}
	for d, sub := range dirs {
		ents = append(ents, ent{"40000", d, r.tree(sub)})
	}
	key := func(e ent) string {
		if e.mode == "40000" {
			return e.name + "/"
		}
		return e.name
	}
	sort.Slice(ents, func(i, j int) bool { return key(ents[i]) < key(ents[j]) })
	var b bytes.Buffer
	for _, e := range ents {
		fmt.Fprintf(&b, "%s %s\x00", e.mode, e.name)
		b.Write(e.oid[:])
	}
	return r.add(git.ObjTree, b.Bytes())
}

func (gs *gitServer) serve(w http.ResponseWriter, req *http.Request) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	path := strings.TrimPrefix(req.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/info/refs"):
		if gs.repos[strings.TrimSuffix(path, "/info/refs")] == nil {
			http.NotFound(w, req)
			return
		}
		pw := &git.PktWriter{}
		pw.Line("# service=git-upload-pack")
		pw.Flush()
		pw.Line("version 2")
		pw.Line("agent=git/2.45.0")
		pw.Line("ls-refs=unborn")
		if gs.noFilt {
			pw.Line("fetch=shallow")
		} else {
			pw.Line("fetch=shallow filter")
		}
		pw.Line("object-format=sha1")
		pw.Flush()
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Write(pw.Bytes())
	case strings.HasSuffix(path, "/git-upload-pack"):
		r := gs.repos[strings.TrimSuffix(path, "/git-upload-pack")]
		body, _ := io.ReadAll(req.Body)
		if r == nil {
			http.NotFound(w, req)
			return
		}
		var lines []string
		pr := git.NewPktReader(bytes.NewReader(body))
		for {
			k, line, err := pr.NextText()
			if err != nil {
				break
			}
			if k == git.PktData {
				lines = append(lines, line)
			}
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		pw := &git.PktWriter{}
		if len(lines) > 0 && lines[0] == "command=ls-refs" {
			var names []string
			for n := range r.tags {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				pw.Line(r.tags[n].String() + " refs/tags/" + n)
			}
			pw.Flush()
			w.Write(pw.Bytes())
			return
		}
		var want git.OID
		filter := false
		for _, l := range lines {
			if s, ok := strings.CutPrefix(l, "want "); ok {
				want, _ = git.ParseOID(s)
			}
			if l == "filter blob:none" {
				filter = true
			}
		}
		gs.fetches++
		if filter {
			gs.filtered++
		}
		pw.Line("packfile")
		pack := r.pack(want, filter)
		for len(pack) > 0 {
			n := min(len(pack), 1000)
			pw.Data(append([]byte{1}, pack[:n]...))
			pack = pack[n:]
		}
		pw.Flush()
		w.Write(pw.Bytes())
	default:
		http.NotFound(w, req)
	}
}

// pack is the pack of want: a blob alone, or a commit with its trees and,
// unless filter, its blobs.
func (r *gitRepo) pack(want git.OID, filter bool) []byte {
	var objs []gitObj
	var walk func(id git.OID)
	walk = func(id git.OID) {
		o, ok := r.objs[id]
		if !ok {
			return
		}
		if o.typ == git.ObjBlob && filter {
			return
		}
		objs = append(objs, o)
		switch o.typ {
		case git.ObjCommit:
			tid, _ := git.ParseOID(strings.TrimPrefix(strings.SplitN(string(o.data), "\n", 2)[0], "tree "))
			walk(tid)
		case git.ObjTree:
			rest := o.data
			for len(rest) > 0 {
				i := bytes.IndexByte(rest, 0)
				var oid git.OID
				copy(oid[:], rest[i+1:i+21])
				walk(oid)
				rest = rest[i+21:]
			}
		}
	}
	walk(want)
	var out bytes.Buffer
	out.WriteString("PACK")
	binary.Write(&out, binary.BigEndian, uint32(2))
	binary.Write(&out, binary.BigEndian, uint32(len(objs)))
	for _, o := range objs {
		size := len(o.data)
		b := []byte{byte(o.typ)<<4 | byte(size&15)}
		size >>= 4
		for size != 0 {
			b[len(b)-1] |= 0x80
			b = append(b, byte(size&0x7f))
			size >>= 7
		}
		out.Write(b)
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(o.data)
		zw.Close()
		out.Write(z.Bytes())
	}
	sum := sha1.Sum(out.Bytes())
	out.Write(sum[:])
	return out.Bytes()
}

// emptyNetrc points NETRC at an empty file, so no test reads a real one.
func emptyNetrc(t *testing.T) {
	p := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NETRC", p)
}
