// Why: fetching a package version needs only its tag's commit, and reading
// its manifest needs only one file; the commands ask the server for no more.
//
// What: LsRefs, Fetch, ReadFetchReply, Tags, FetchTag and FetchFile, with
// FileAt.
//
// How: ls-refs over refs/tags/v, a fetch of one commit at depth 1, the fetch
// reply read as a stream from the response body (no copy of the whole
// reply). FetchFile, where the server offers fetch's filter feature, fetches
// the commit with "filter blob:none" and then the one blob, else the whole
// commit.

package git

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// command starts a request body: the command and the capabilities the
// server advertised (git's client sends agent and object-format only to a
// server that lists them).
func (c *Client) command(r *Remote, cmd string) *PktWriter {
	w := &PktWriter{}
	w.Line("command=" + cmd)
	if _, ok := r.Caps["agent"]; ok {
		w.Line("agent=" + c.agent())
	}
	if _, ok := r.Caps["object-format"]; ok {
		w.Line("object-format=sha1")
	}
	w.Delim()
	return w
}

// postOpen POSTs body to <base>/git-upload-pack and returns the reply
// body as a stream capped at limit bytes.
func (c *Client) postOpen(r *Remote, body []byte, limit int64) (io.ReadCloser, error) {
	u := *r.base
	u.Path += "/git-upload-pack"
	u.RawPath = ""
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errorf(ReasonInvalid, "%s: %v", redactURL(r.base), err)
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")
	out, _, err := c.open(req, limit, "application/x-git-upload-pack-result", r.authHost)
	return out, err
}

// LsRefs lists the refs under prefix, with annotated tags peeled. Refs a
// server returns outside prefix are dropped.
func (c *Client) LsRefs(r *Remote, prefix string) ([]Ref, error) {
	w := c.command(r, "ls-refs")
	w.Line("peel")
	w.Line("ref-prefix " + prefix)
	w.Flush()
	body, err := c.postOpen(r, w.Bytes(), maxLsRefs)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	refs, err := readLsRefs(body)
	if err != nil {
		return nil, err
	}
	out := refs[:0]
	for _, ref := range refs {
		if strings.HasPrefix(ref.Name, prefix) {
			out = append(out, ref)
		}
	}
	return out, nil
}

// ParseLsRefs reads an ls-refs reply: "<oid> <ref>[ <attr>]*" lines and a
// flush; the attribute "peeled:<oid>" is kept, others ignored.
func ParseLsRefs(body []byte) ([]Ref, error) { return readLsRefs(bytes.NewReader(body)) }

func readLsRefs(body io.Reader) ([]Ref, error) {
	pr := NewPktReader(body)
	var refs []Ref
	for {
		k, line, err := pr.NextText()
		if err == io.EOF {
			return nil, corrupt("the ls-refs reply ends before its flush")
		}
		if err != nil {
			return nil, err
		}
		if k == PktFlush {
			return refs, nil
		}
		if k != PktData {
			return nil, corrupt("the ls-refs reply holds a delimiter")
		}
		f := strings.Split(line, " ")
		if len(f) < 2 {
			return nil, corrupt("the ls-refs line %s is malformed", clean(line))
		}
		o, ok := ParseOID(f[0])
		if !ok {
			if len(f[0]) == 64 {
				return nil, errorf(ReasonProtocol, "the repository uses SHA-256 object ids; only SHA-1 repositories are read; use a url dependency with an archive")
			}
			return nil, corrupt("the ls-refs line %s has a malformed id", clean(line))
		}
		ref := Ref{Name: f[1], OID: o}
		for _, a := range f[2:] {
			if v, ok := strings.CutPrefix(a, "peeled:"); ok {
				p, ok := ParseOID(v)
				if !ok {
					return nil, corrupt("the ls-refs line %s has a malformed peeled id", clean(line))
				}
				ref.Peeled = p
			}
		}
		refs = append(refs, ref)
	}
}

// fetchOpts are a fetch request's arguments beyond want.
type fetchOpts struct {
	deepen bool   // "deepen 1", when the server offers shallow
	filter string // "filter <spec>", when the server offers filter
}

// Fetch fetches want at depth 1 (when the server offers shallow) and
// parses the pack.
func (c *Client) Fetch(r *Remote, want OID) (*Store, error) {
	return c.fetch(r, want, fetchOpts{deepen: true})
}

func (c *Client) fetch(r *Remote, want OID, o fetchOpts) (*Store, error) {
	lim := c.Limits.withDefaults()
	w := c.command(r, "fetch")
	w.Line("want " + want.String())
	if o.deepen && r.fetchFeature("shallow") {
		w.Line("deepen 1")
	}
	if o.filter != "" {
		w.Line("filter " + o.filter)
	}
	w.Line("ofs-delta")
	w.Line("no-progress")
	w.Line("done")
	w.Flush()
	// The reply holds the pack (compressed, so under MaxBytes for any pack
	// the limits admit) and its pkt-line and side-band framing.
	body, err := c.postOpen(r, w.Bytes(), lim.MaxBytes+lim.MaxBytes/8+(1<<20))
	if err != nil {
		return nil, err
	}
	defer body.Close()
	pack, err := ReadFetchReply(body, lim.MaxBytes)
	if err != nil {
		return nil, err
	}
	return ParsePack(pack, lim)
}

// ReadFetchReply reads a fetch reply's sections from body and returns the
// pack carried by its packfile section's side band 1. Band 2 (progress) is
// ignored; band 3 is an error with its text, sanitised and capped. Sections
// before packfile (acknowledgments, shallow-info, wanted-refs,
// packfile-uris) are skipped. An ERR line anywhere is ReasonNetwork
// (PktReader).
func ReadFetchReply(body io.Reader, maxPack int64) ([]byte, error) {
	pr := NewPktReader(body)
	for {
		k, line, err := pr.NextText()
		if err == io.EOF {
			return nil, corrupt("the fetch reply holds no packfile section")
		}
		if err != nil {
			return nil, err
		}
		if k != PktData {
			return nil, corrupt("the fetch reply holds no packfile section")
		}
		if line == "packfile" {
			break
		}
		// Another section: skip to its delimiter.
		for {
			k, _, err := pr.Next()
			if err == io.EOF {
				return nil, corrupt("the fetch reply's %s section is not closed", clean(line))
			}
			if err != nil {
				return nil, err
			}
			if k == PktDelim {
				break
			}
			if k != PktData {
				return nil, corrupt("the fetch reply's %s section ends with no packfile", clean(line))
			}
		}
	}
	// The pack arrives in fixed chunks and is joined once at the end into
	// a buffer of its exact size, so it costs twice the pack at the join
	// and once after, with no growth overshoot.
	const packChunk = 1 << 20
	var chunks [][]byte
	var chunk []byte
	var total int64
	for {
		k, b, err := pr.Next()
		if err == io.EOF {
			return nil, corrupt("the packfile section ends before its flush")
		}
		if err != nil {
			return nil, err
		}
		if k == PktFlush || k == PktResponseEnd {
			pack := make([]byte, 0, total)
			for _, c := range chunks {
				pack = append(pack, c...)
			}
			return append(pack, chunk...), nil
		}
		if k != PktData || len(b) == 0 {
			return nil, corrupt("the packfile section holds an empty or delimiter line")
		}
		switch b[0] {
		case 1:
			if total+int64(len(b)-1) > maxPack {
				return nil, errorf(ReasonLimit, "the pack is larger than %d bytes", maxPack)
			}
			if len(chunk)+len(b)-1 > cap(chunk) {
				if len(chunk) > 0 {
					chunks = append(chunks, chunk)
				}
				chunk = make([]byte, 0, packChunk)
			}
			chunk = append(chunk, b[1:]...)
			total += int64(len(b) - 1)
		case 2:
		case 3:
			return nil, errorf(ReasonNetwork, "the server failed the fetch: %s", clean(strings.TrimSpace(string(b[1:]))))
		default:
			return nil, corrupt("the packfile section uses side band %d", b[0])
		}
	}
}

// Tags lists the version tags (refs/tags/v...) of the repository at rawURL.
func (c *Client) Tags(rawURL string) ([]Ref, error) {
	r, err := c.Discover(rawURL)
	if err != nil {
		return nil, err
	}
	return c.LsRefs(r, "refs/tags/v")
}

// findTag runs discovery and ls-refs and returns the tag's ref.
func (c *Client) findTag(rawURL, tag string) (*Remote, *Ref, error) {
	if !strings.HasPrefix(tag, "v") || strings.ContainsAny(tag, " \n\x00") {
		return nil, nil, errorf(ReasonInvalid, "the tag %s is not a version tag v...", clean(tag))
	}
	r, err := c.Discover(rawURL)
	if err != nil {
		return nil, nil, err
	}
	refs, err := c.LsRefs(r, "refs/tags/v")
	if err != nil {
		return nil, nil, err
	}
	for i := range refs {
		if refs[i].Name == "refs/tags/"+tag {
			return r, &refs[i], nil
		}
	}
	return nil, nil, errorf(ReasonNotFound, "%s has no tag %s", redactURL(r.base), tag)
}

// peelChecked peels ref in st and checks the commit is the one ls-refs
// named.
func peelChecked(st *Store, ref *Ref, tag string) (OID, error) {
	commit, err := st.Peel(ref.OID)
	if err != nil {
		return OID{}, err
	}
	if commit != ref.Commit() {
		return OID{}, corrupt("the tag %s peels to %s in the pack, but ls-refs names %s", tag, commit, ref.Commit())
	}
	return commit, nil
}

// FetchTag fetches the tag (for example "v1.2.3") of the repository at
// rawURL at depth 1 and walks its commit's tree. The commit the pack's tag
// peels to must be the one ls-refs named.
func (c *Client) FetchTag(rawURL, tag string) (*Snapshot, error) {
	r, ref, err := c.findTag(rawURL, tag)
	if err != nil {
		return nil, err
	}
	st, err := c.Fetch(r, ref.OID)
	if err != nil {
		return nil, err
	}
	commit, err := peelChecked(st, ref, tag)
	if err != nil {
		return nil, err
	}
	files, err := st.Files(commit, c.Limits)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Ref: *ref, Commit: commit, Files: files}, nil
}

// FileAt is one file of a tag, read without its tree's other blobs.
type FileAt struct {
	Ref    Ref
	Commit OID
	Blob   OID
	Data   []byte
	// Filtered: the commit came with "filter blob:none" and the blob by a
	// second fetch; false when the whole commit was fetched, because the
	// server offers no filter or refused or failed the blob want.
	Filtered bool
}

// FetchFile reads one regular file (a slash path such as "pkg.mk") of the
// tag: where the server's fetch offers filter, the commit at depth 1 with
// "filter blob:none" and then the file's blob alone, by its id; otherwise the
// whole commit. The blob's id is recomputed, so the bytes are the ones the
// commit's tree names.
func (c *Client) FetchFile(rawURL, tag, path string) (*FileAt, error) {
	r, ref, err := c.findTag(rawURL, tag)
	if err != nil {
		return nil, err
	}
	filtered := r.fetchFeature("filter")
	o := fetchOpts{deepen: true}
	if filtered {
		o.filter = "blob:none"
	}
	st, err := c.fetch(r, ref.OID, o)
	if err != nil {
		return nil, err
	}
	commit, err := peelChecked(st, ref, tag)
	if err != nil {
		return nil, err
	}
	e, err := st.Lookup(commit, path, c.Limits)
	if err != nil {
		return nil, err
	}
	blob, ok := st.Get(e.OID)
	if !ok && filtered {
		// A server that refuses or fails the bare blob want costs
		// one whole fetch, not the read: the commit again without filter.
		if bst, err := c.fetch(r, e.OID, fetchOpts{}); err == nil {
			blob, ok = bst.Get(e.OID)
		}
		if !ok {
			filtered = false
			whole, err := c.fetch(r, ref.OID, fetchOpts{deepen: true})
			if err != nil {
				return nil, err
			}
			if _, err := peelChecked(whole, ref, tag); err != nil {
				return nil, err
			}
			blob, ok = whole.Get(e.OID)
		}
	}
	if !ok || blob.Type != ObjBlob {
		return nil, corrupt("the file %s (blob %s) is not in the pack", clean(path), e.OID)
	}
	return &FileAt{Ref: *ref, Commit: commit, Blob: e.OID, Data: blob.Data, Filtered: filtered}, nil
}
