// Why: a url package has no version to name, so its identity is its
// content; the hash that pins it lives in pkg.sum, and older manifests
// that pin the archive in the Dep must keep working.
//
// What: depWalk.urlTree (fetch and check one url package) and getURL (the
// GET of its archive).
//
// How: a url package's tree hash is a pkg.sum line, `<url> - -
// tree-sha256:<hex>`, recorded when it is first fetched and checked on
// every later fetch (E0202 sum-mismatch). A Dep written with an archive
// hash (`hash: "sha256:..."`, the older form) keeps working: the archive
// is checked against it, with a note naming mk fix, which moves it into
// pkg.sum (fix_pkgsum.go). A Dep with no hash has its archive fetched and
// its tree checked against pkg.sum alone.

package driver

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"mk/internal/fetch"
)

// urlTree fetches and unpacks d's archive, checks its tree against
// pkg.sum and records the line pkg.sum lacks.
func (dw *depWalk) urlTree(d Dep) (*fetch.Tree, error) {
	var data []byte
	var err error
	if d.Hash != "" {
		h, _ := fetch.ParseHash(d.Hash)
		data, _, err = dw.fetcherFor().Archive(d.URL, h)
		dw.note("note: dependency %s: hash is an archive hash in pkg.mk; mk fix moves it into pkg.sum", d.Name)
	} else {
		if sum := dw.loadSum(dw.root); sum != nil && checkSumURL(d.URL) == nil && (dw.s.cfg.Offline || dw.s.cfg.Locked) {
			if _, ok := sum.get(d.URL, "", false); !ok {
				return nil, &fetch.Error{Reason: reasonSumMissing, Msg: sumMissingMsg(dw.s.cfg, d.URL)}
			}
		}
		data, err = dw.getURL(d.URL)
	}
	if err != nil {
		return nil, err
	}
	tr, err := fetch.Unpack(data, fetch.Limits{})
	if err != nil {
		return nil, err
	}
	sum := dw.loadSum(dw.root)
	if sum == nil {
		return nil, &fetch.Error{Reason: "invalid", Msg: "pkg.sum does not read"}
	}
	l := sumLine{Source: d.URL, Hash: tr.Hash().String()}
	if checkSumURL(d.URL) != nil {
		return tr, nil // validateDepSource refused it unless a hash names it
	}
	if reason, old := sum.check(l); reason != "" {
		return nil, &fetch.Error{Reason: reason, Msg: fmt.Sprintf("the archive's tree has %s, but pkg.sum records %s", l.Hash, old.Hash)}
	}
	if _, ok := sum.get(l.Source, "", false); !ok && dw.s.cfg.Locked {
		// An archive hash in the Dep checks the archive, but --locked also
		// wants pkg.sum complete.
		return nil, &fetch.Error{Reason: reasonSumMissing, Msg: sumMissingMsg(dw.s.cfg, d.URL)}
	}
	if sum.add(l) {
		dw.sumDirt = true
	}
	return tr, nil
}

// getURL is one GET of a url package's archive with no archive hash to check
// (the tree is checked against pkg.sum), capped as fetch caps an archive.
// The tree is cached by its hash once pkg.sum records it (deps_walk.go
// source).
func (dw *depWalk) getURL(raw string) ([]byte, error) {
	if err := fetch.CheckURL(raw); err != nil {
		return nil, err
	}
	if dw.s.cfg.Offline {
		return nil, &fetch.Error{Reason: fetch.ReasonOffline, Msg: fmt.Sprintf("archive %s is not in the cache, and the build is offline", raw)}
	}
	client := dw.fetcherFor().Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Get(raw)
	if err != nil {
		return nil, &fetch.Error{Reason: "fetch", Msg: fmt.Sprintf("fetch %s: %v", raw, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &fetch.Error{Reason: "fetch", Msg: fmt.Sprintf("fetch %s: the server answered %s", raw, resp.Status)}
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(resp.Body, fetch.DefaultMaxArchive+1))
	if err != nil {
		return nil, &fetch.Error{Reason: "fetch", Msg: fmt.Sprintf("fetch %s: %v", raw, err)}
	}
	if n > fetch.DefaultMaxArchive {
		return nil, &fetch.Error{Reason: "unsafe-archive", Msg: fmt.Sprintf("fetch %s: the archive is larger than %d bytes", raw, int64(fetch.DefaultMaxArchive))}
	}
	return buf.Bytes(), nil
}
