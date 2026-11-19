// Why: a package's url dependencies are archives on the network; the
// compiler must get each one once, prove it is the archive the manifest
// names, and never unpack a hostile archive onto the disk.
//
// What: Hash, ParseHash and Sum (an archive's SHA-256), CheckURL, Error and
// its reasons, and Fetcher with Archive, which serves from the content cache
// or by one GET.
//
// How: an archive's SHA-256 is checked against the manifest's hash before it
// is cached or read, then it is unpacked in memory (unpack.go). A git tag
// comes through internal/git (git.go); both become a Tree whose tree hash
// (treehash.go) is checked before any file is written. The package uses
// internal/git, cache and source, and Go's net/http, archive/tar,
// archive/zip, compress/gzip and crypto/sha256.

// Package fetch fetches, verifies, caches and unpacks the archives of url
// dependencies. It reports failures as *Error values with a reason; the
// driver turns each into E0202 at the pkg.Dep that named the archive.
package fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mk/internal/cache"
	"mk/internal/git"
	"mk/internal/source"
)

// Hash is the SHA-256 of an archive's bytes.
type Hash [32]byte

// hashPrefix is the only hash algorithm a manifest may name (BLAKE3 is not
// in Go's standard library).
const hashPrefix = "sha256:"

// ParseHash reads a manifest hash, "sha256:" and 64 hex digits of either
// case.
func ParseHash(s string) (Hash, bool) {
	var h Hash
	hexPart, ok := strings.CutPrefix(s, hashPrefix)
	if !ok || len(hexPart) != 64 {
		return h, false
	}
	if _, err := hex.Decode(h[:], []byte(hexPart)); err != nil {
		return h, false
	}
	return h, true
}

// String is the canonical spelling, "sha256:" and lowercase hex.
func (h Hash) String() string { return hashPrefix + hex.EncodeToString(h[:]) }

// Sum is the Hash of data.
func Sum(data []byte) Hash { return Hash(sha256.Sum256(data)) }

// Reasons of an Error, which the driver puts in E0202's reason fact.
const (
	ReasonInvalid       = "invalid"        // a malformed url or hash
	ReasonFetch         = "fetch"          // the network or the server failed
	ReasonOffline       = "offline"        // offline, and the archive is not in the cache
	ReasonHashMismatch  = "hash-mismatch"  // the bytes fetched are not the archive the hash names
	ReasonUnsafeArchive = "unsafe-archive" // a path escape, a link, or a limit
)

// Error is a fetch or unpack failure: a reason and a message that names no
// absolute path.
type Error struct {
	Reason string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func errorf(reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Msg: fmt.Sprintf(format, args...)}
}

// CheckURL accepts an https URL with a host, or an http one whose host is
// a loopback address or "localhost" (a local server; the hash is checked
// either way, so plain http elsewhere would protect the bytes but not
// their confidentiality, and the manifest names https).
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errorf(ReasonInvalid, "url %q is not an absolute https URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback(u.Hostname()) {
			return nil
		}
		return errorf(ReasonInvalid, "url %q: a dependency is fetched over https (http only from this machine)", raw)
	}
	return errorf(ReasonInvalid, "url %q: the scheme must be https", raw)
}

// loopback reports whether host names this machine.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// DefaultMaxArchive caps an archive's size, fetched or cached: 1 GiB.
const DefaultMaxArchive = 1 << 30

// Fetcher gets archives from the cache or the network.
type Fetcher struct {
	// Store is the content cache; nil keeps nothing, so every Archive call
	// fetches, and offline ones fail.
	Store *cache.Store
	// Client does the GET; nil means a client with a 10 minute timeout.
	Client *http.Client
	// Offline: never touch the network; an archive not in the store is
	// ReasonOffline.
	Offline bool
	// MaxArchive caps the archive's size in bytes; 0 means DefaultMaxArchive.
	// For a git fetch it caps the pack and the bytes of the tree's files.
	MaxArchive int64
	// Agent is the git client's agent, "mk/<version>"; "" means "mk".
	Agent string
	// Netrc gives git credentials; nil reads the netrc file NETRC, ~/.netrc
	// or %USERPROFILE%\_netrc names.
	Netrc *git.Netrc
}

// storeKey is the cache key of the archive whose hash is h.
func storeKey(h Hash) source.Hash { return source.HashString("mk.fetch.archive.v1", h.String()) }

// Archive returns the bytes of the archive at rawURL whose SHA-256 is want:
// from the store when an entry there hashes to want (fetched false), else
// by one GET (fetched true), checked against want before it is returned or
// stored. A store entry with other bytes is a miss.
func (f *Fetcher) Archive(rawURL string, want Hash) (data []byte, fetched bool, err error) {
	if err := CheckURL(rawURL); err != nil {
		return nil, false, err
	}
	if f.Store != nil {
		if b, ok, err := f.Store.Get(cache.KindFetch, storeKey(want)); err == nil && ok && Sum(b) == want {
			return b, false, nil
		}
	}
	if f.Offline {
		return nil, false, errorf(ReasonOffline, "archive %s (%s) is not in the cache, and the build is offline", rawURL, want)
	}
	b, err := f.get(rawURL)
	if err != nil {
		return nil, false, err
	}
	if got := Sum(b); got != want {
		return nil, false, errorf(ReasonHashMismatch, "archive %s has hash %s, but the manifest says %s", rawURL, got, want)
	}
	if f.Store != nil {
		// A write the store drops is not an error: the next build fetches
		// again.
		_ = f.Store.Put(cache.KindFetch, storeKey(want), b)
	}
	return b, true, nil
}

// get is one GET of rawURL, capped at MaxArchive bytes.
func (f *Fetcher) get(rawURL string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	limit := f.MaxArchive
	if limit <= 0 {
		limit = DefaultMaxArchive
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, errorf(ReasonFetch, "fetch %s: %v", rawURL, unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errorf(ReasonFetch, "fetch %s: the server answered %s", rawURL, resp.Status)
	}
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errorf(ReasonFetch, "fetch %s: %v", rawURL, err)
	}
	if n > limit {
		return nil, errorf(ReasonUnsafeArchive, "fetch %s: the archive is larger than %d bytes", rawURL, limit)
	}
	return buf.Bytes(), nil
}

// unwrapURLError drops net/http's "Get <url>:" prefix, which the message
// already says.
func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
