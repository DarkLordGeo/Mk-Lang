// Why: packages are fetched from git hosts, and the transport must never
// send a user's credentials anywhere the user did not name, whatever the
// server's redirects say.
//
// What: Client, Remote, Ref and Snapshot; CheckURL and the redirect and hop
// checks (checkRedirect, checkHop); URL redaction for errors.
//
// How: git's protocol version 2 over smart HTTP.
// Discovery is a GET of <url>/info/refs?service=git-upload-pack with
// Git-Protocol: version=2; ls-refs and fetch are POSTs to
// <url>/git-upload-pack. A reply without "version 2", or with an
// object-format other than sha1, is ReasonProtocol. Credentials from a
// netrc file go as basic authentication over https to the host and port
// the caller named only: Go's client keeps Authorization on a redirect to
// the same host name on another port or to a subdomain, so CheckRedirect
// drops it on any other scheme or host:port, and every request, the POSTs
// to a redirected base included, attaches it only for that host:port.
// A hop to a URL the client would not use, or to http on this machine
// from anywhere else, is refused as a network fault. Only discovery
// follows redirects; the POSTs go to the URL discovery ended at
// (commands.go).

package git

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks protocol version 2 to smart HTTP servers.
type Client struct {
	// HTTP does the requests; nil means a client with a 10 minute timeout.
	// Its CheckRedirect is replaced (see the file comment).
	HTTP *http.Client
	// Agent is sent as the agent capability and the User-Agent, for
	// example "mk/0.1.0"; "" means "mk".
	Agent string
	// Netrc gives credentials; nil sends none.
	Netrc *Netrc
	// Limits bound the pack and the tree (zero fields: the defaults).
	Limits Limits
}

// Remote is a repository after discovery: its base URL and capabilities.
type Remote struct {
	base *url.URL
	// authHost is the host and port of the URL the caller gave: the only
	// one credentials go to, whatever discovery's redirects did.
	authHost string
	// Caps maps each capability to its value ("" for none).
	Caps map[string]string
}

// URL is the base URL requests go to (discovery's final URL).
func (r *Remote) URL() string { return r.base.String() }

// fetchFeature reports whether the fetch capability lists feature.
func (r *Remote) fetchFeature(feature string) bool {
	v, ok := r.Caps["fetch"]
	return ok && contains(strings.Fields(v), feature)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Ref is one line of an ls-refs reply.
type Ref struct {
	Name   string
	OID    OID
	Peeled OID // the commit an annotated tag peels to; zero for others
}

// Commit is the commit Ref names: Peeled for an annotated tag, else OID.
func (r Ref) Commit() OID {
	if !r.Peeled.IsZero() {
		return r.Peeled
	}
	return r.OID
}

// Snapshot is a tag fetched and walked.
type Snapshot struct {
	Ref    Ref
	Commit OID
	Files  []File
}

// Reply size caps for the replies that hold no pack.
const (
	maxDiscovery = 1 << 20
	maxLsRefs    = 64 << 20
)

// CheckURL accepts an https URL with a host, or an http one to a loopback
// host, with no user info, query or fragment.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errorf(ReasonInvalid, "%q is not an absolute https URL", redact(raw))
	}
	return checkURL(u)
}

func checkURL(u *url.URL) error {
	if u.RawQuery != "" || u.Fragment != "" {
		return errorf(ReasonInvalid, "the repository URL %s has a query or a fragment", redactURL(u))
	}
	return checkHop(u)
}

// checkHop is checkURL without the query rule: every request, redirects
// included, goes over https, or http to this machine, and carries no user
// info.
func checkHop(u *url.URL) error {
	if u.User != nil {
		return errorf(ReasonInvalid, "the repository URL holds credentials; give them in a netrc file")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback(u.Hostname()) {
			return nil
		}
	}
	return errorf(ReasonInvalid, "the repository URL %s is not https (http only to this machine)", redactURL(u))
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redact drops anything that may be a credential from a URL text.
func redact(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return redactURL(u)
	}
	if i := strings.LastIndexByte(raw, '@'); i >= 0 {
		return raw[i+1:]
	}
	return raw
}

func redactURL(u *url.URL) string {
	c := *u
	c.User = nil
	c.RawQuery = ""
	c.Fragment = ""
	return c.String()
}

func (c *Client) agent() string {
	if c.Agent == "" {
		return "mk"
	}
	return c.Agent
}

// httpClient is c.HTTP with this package's redirect rule.
func (c *Client) httpClient() *http.Client {
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	} else {
		hc.Timeout = 10 * time.Minute
	}
	hc.CheckRedirect = checkRedirect
	return &hc
}

// checkRedirect follows at most 10 redirects of a GET, each to a URL
// checkHop accepts, http only to loopback and only when the first request
// was to loopback, and drops Authorization unless the hop keeps https and
// the first request's host and port. A refused hop is ReasonNetwork.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errorf(ReasonNetwork, "more than 10 redirects")
	}
	if via[0].Method != http.MethodGet {
		return http.ErrUseLastResponse
	}
	first := via[0].URL
	if err := checkHop(req.URL); err != nil {
		return errorf(ReasonNetwork, "a redirect is refused: %s", err.(*Error).Msg)
	}
	if req.URL.Scheme == "http" && !loopback(first.Hostname()) {
		return errorf(ReasonNetwork, "a redirect from %s to plain http on this machine is refused", redactURL(first))
	}
	if req.URL.Scheme != "https" || first.Scheme != "https" || canonHost(req.URL) != canonHost(first) {
		req.Header.Del("Authorization")
	}
	return nil
}

// canonHost is u's lowercase host and port, the port made explicit.
func canonHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// capReader fails with ReasonLimit once more than left bytes are read.
type capReader struct {
	r     io.Reader
	left  int64
	limit int64
	where string
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return 0, errorf(ReasonLimit, "%s: the reply is larger than %d bytes", c.where, c.limit)
	}
	if err != nil && err != io.EOF {
		return n, errorf(ReasonNetwork, "%s: %v", c.where, err)
	}
	return n, err
}

// open sends req, with credentials only when the scheme is https and the
// host and port are authHost's (the URL the caller gave), and returns the
// reply body, capped at limit bytes, and the URL the request ended at.
func (c *Client) open(req *http.Request, limit int64, wantType, authHost string) (io.ReadCloser, *url.URL, error) {
	req.Header.Set("Git-Protocol", "version=2")
	req.Header.Set("User-Agent", c.agent())
	if req.URL.Scheme == "https" && canonHost(req.URL) == authHost {
		if login, pass, ok := c.Netrc.Lookup(req.URL.Host); ok {
			req.SetBasicAuth(login, pass)
		}
	}
	where := req.Method + " " + redactURL(req.URL)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ge, ok := unwrapGit(err); ok {
			return nil, nil, ge
		}
		return nil, nil, errorf(ReasonNetwork, "%s: %v", where, unwrapURLError(err))
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		msg := fmt.Sprintf("%s: the server answered %d %s", where, resp.StatusCode, http.StatusText(resp.StatusCode))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msg += " (a private repository needs a netrc entry for its host)"
		}
		return nil, nil, errorf(ReasonNetwork, "%s", msg)
	}
	if ct := resp.Header.Get("Content-Type"); wantType != "" && !strings.HasPrefix(ct, wantType) {
		resp.Body.Close()
		return nil, nil, errorf(ReasonProtocol, "%s: the reply is %s, not %s: the server does not speak git's smart HTTP protocol; use a url dependency with an archive", where, clean(ct), wantType)
	}
	body := struct {
		io.Reader
		io.Closer
	}{&capReader{r: resp.Body, left: limit, limit: limit, where: where}, resp.Body}
	return body, resp.Request.URL, nil
}

// do is open with the whole body read (replies that hold no pack).
func (c *Client) do(req *http.Request, limit int64, wantType, authHost string) ([]byte, *url.URL, error) {
	body, final, err := c.open(req, limit, wantType, authHost)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		if ge, ok := err.(*Error); ok {
			return nil, nil, ge
		}
		return nil, nil, errorf(ReasonNetwork, "%v", err)
	}
	return b, final, nil
}

func unwrapGit(err error) (*Error, bool) {
	for err != nil {
		if ge, ok := err.(*Error); ok {
			return ge, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = u.Unwrap()
	}
	return nil, false
}

func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// Discover runs discovery against the repository at rawURL.
func (c *Client) Discover(rawURL string) (*Remote, error) {
	base, err := url.Parse(strings.TrimSuffix(rawURL, "/"))
	if err != nil || base.Host == "" {
		return nil, errorf(ReasonInvalid, "%q is not an absolute https URL", redact(rawURL))
	}
	if err := checkURL(base); err != nil {
		return nil, err
	}
	u := *base
	u.Path += "/info/refs"
	u.RawPath = ""
	u.RawQuery = "service=git-upload-pack"
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errorf(ReasonInvalid, "%s: %v", redactURL(base), err)
	}
	authHost := canonHost(base)
	body, final, err := c.do(req, maxDiscovery, "application/x-git-upload-pack-advertisement", authHost)
	if err != nil {
		return nil, err
	}
	caps, err := ParseDiscovery(body)
	if err != nil {
		if ge, ok := err.(*Error); ok && ge.Reason == ReasonProtocol {
			ge.Msg = redactURL(base) + ": " + ge.Msg
		}
		return nil, err
	}
	r := &Remote{Caps: caps, authHost: authHost}
	nb := *final
	nb.RawQuery = ""
	nb.Path = strings.TrimSuffix(nb.Path, "/info/refs")
	nb.RawPath = ""
	r.base = &nb
	return r, nil
}

// ParseDiscovery reads a discovery reply: an optional "# service=" line and
// flush (git's http-backend sends them), then "version 2", the capabilities,
// and a flush.
func ParseDiscovery(body []byte) (map[string]string, error) {
	pr := NewPktReader(bytes.NewReader(body))
	k, line, err := pr.NextText()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if k == PktData && strings.HasPrefix(line, "# service=") {
		if line != "# service=git-upload-pack" {
			return nil, corrupt("the discovery reply is for %s", clean(strings.TrimPrefix(line, "# service=")))
		}
		if k, _, err = pr.NextText(); err != nil || k != PktFlush {
			return nil, corrupt("the discovery reply's service line is not followed by a flush")
		}
		k, line, err = pr.NextText()
		if err != nil && err != io.EOF {
			return nil, err
		}
	}
	if err == io.EOF || k != PktData || line != "version 2" {
		return nil, errorf(ReasonProtocol, "the server does not offer git protocol version 2; use a url dependency with an archive")
	}
	caps := map[string]string{}
	for {
		k, line, err := pr.NextText()
		if err == io.EOF {
			return nil, corrupt("the discovery reply ends before its flush")
		}
		if err != nil {
			return nil, err
		}
		if k == PktFlush {
			break
		}
		if k != PktData {
			return nil, corrupt("the discovery reply holds a delimiter")
		}
		key, val, _ := strings.Cut(line, "=")
		caps[key] = val
	}
	if f, ok := caps["object-format"]; ok && f != "sha1" {
		return nil, errorf(ReasonProtocol, "the repository's object format is %s; only SHA-1 repositories are read; use a url dependency with an archive", clean(f))
	}
	if _, ok := caps["ls-refs"]; !ok {
		return nil, errorf(ReasonProtocol, "the server does not offer ls-refs; use a url dependency with an archive")
	}
	if _, ok := caps["fetch"]; !ok {
		return nil, errorf(ReasonProtocol, "the server does not offer fetch; use a url dependency with an archive")
	}
	return caps, nil
}
