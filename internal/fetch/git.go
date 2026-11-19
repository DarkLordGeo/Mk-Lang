// Why: a source dependency names a git tag rather than an archive; its tree
// must pass the same checks as an archive before Mk uses it.
//
// What: GitTree, SourceURL, FromGit, and Fetcher's GitTag and GitTags, plus
// the reasons ReasonProtocol and ReasonNoVersion.
//
// How: FromGit makes a Tree of the walked git tree with Unpack's refusals
// (CheckPath, the file and byte limits, names equal after case folding, a
// file that is also a directory) and no top directory stripped. fromGit maps
// internal/git's failures to E0202 reasons: no protocol version 2 or a
// SHA-256 repository is `protocol`; a network failure, a malformed reply or
// pack and an exceeded limit are `fetch`; a link, a submodule or a refused
// name is `unsafe-archive`; a missing tag is `no-version`.

package fetch

import (
	"errors"
	"os"
	"runtime"

	"mk/internal/git"
)

// Reasons of a git fetch.
const (
	ReasonProtocol  = "protocol"   // no protocol version 2, or not SHA-1
	ReasonNoVersion = "no-version" // the tag is not in the repository
)

// GitTree is a tag of a repository, fetched and checked.
type GitTree struct {
	Commit string // 40 hex digits
	Tree   *Tree
	Hash   TreeHash
}

// SourceURL is the https URL of a manifest's source, "host/path".
func SourceURL(source string) string { return "https://" + source }

// FromGit makes a Tree of a walked git tree, under lim (zero fields: the
// defaults), with Unpack's refusals and no top directory stripped.
func FromGit(files []git.File, lim Limits) (*Tree, error) {
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = DefaultMaxFiles
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultMaxBytes
	}
	u := &unpacker{lim: lim}
	for _, f := range files {
		p, err := u.name(f.Path)
		if err != nil {
			return nil, err
		}
		if err := u.count(); err != nil {
			return nil, err
		}
		if err := u.add(p, f.Data, int64(len(f.Data))); err != nil {
			return nil, err
		}
	}
	return u.finish(false)
}

// gitLimits are the git client's limits for f: the byte limit is f's
// archive cap and the file limit Unpack's.
func (f *Fetcher) gitLimits() git.Limits {
	limit := f.MaxArchive
	if limit <= 0 {
		limit = DefaultMaxArchive
	}
	return git.Limits{MaxBytes: limit, MaxFiles: DefaultMaxFiles}
}

// netrc is f.Netrc, else the netrc file the environment names.
func (f *Fetcher) netrc() (*git.Netrc, error) {
	if f.Netrc != nil {
		return f.Netrc, nil
	}
	return git.LoadNetrc(git.NetrcPath(os.Getenv, runtime.GOOS == "windows"))
}

func (f *Fetcher) gitClient() (*git.Client, error) {
	n, err := f.netrc()
	if err != nil {
		return nil, fromGit(err, "")
	}
	agent := f.Agent
	if agent == "" {
		agent = "mk"
	}
	return &git.Client{HTTP: f.Client, Agent: agent, Netrc: n, Limits: f.gitLimits()}, nil
}

// GitTag fetches the tag (for example "v1.2.3") of the repository at
// rawURL (SourceURL of a manifest's source), walks its commit's tree and
// returns it as a checked Tree with its hash. Nothing is written or cached;
// an offline Fetcher fails with ReasonOffline.
func (f *Fetcher) GitTag(rawURL, tag string) (*GitTree, error) {
	if err := git.CheckURL(rawURL); err != nil {
		return nil, fromGit(err, "")
	}
	if f.Offline {
		return nil, errorf(ReasonOffline, "%s %s is not in the cache, and the build is offline", rawURL, tag)
	}
	c, err := f.gitClient()
	if err != nil {
		return nil, err
	}
	snap, err := c.FetchTag(rawURL, tag)
	if err != nil {
		return nil, fromGit(err, rawURL+" "+tag+": ")
	}
	tr, err := FromGit(snap.Files, Limits{})
	if err != nil {
		return nil, err
	}
	return &GitTree{Commit: snap.Commit.String(), Tree: tr, Hash: tr.Hash()}, nil
}

// GitTags lists the version tags (refs/tags/v...) of the repository at
// rawURL, with the commit each names.
func (f *Fetcher) GitTags(rawURL string) ([]git.Ref, error) {
	if err := git.CheckURL(rawURL); err != nil {
		return nil, fromGit(err, "")
	}
	if f.Offline {
		return nil, errorf(ReasonOffline, "the tags of %s are needed, and the build is offline", rawURL)
	}
	c, err := f.gitClient()
	if err != nil {
		return nil, err
	}
	refs, err := c.Tags(rawURL)
	if err != nil {
		return nil, fromGit(err, rawURL+": ")
	}
	return refs, nil
}

// fromGit is an internal/git failure as an Error with an E0202 reason.
func fromGit(err error, prefix string) error {
	var ge *git.Error
	if !errors.As(err, &ge) {
		return errorf(ReasonFetch, "%s%v", prefix, err)
	}
	reason := ReasonFetch
	switch ge.Reason {
	case git.ReasonProtocol:
		reason = ReasonProtocol
	case git.ReasonRefused:
		reason = ReasonUnsafeArchive
	case git.ReasonNotFound:
		reason = ReasonNoVersion
	case git.ReasonInvalid:
		reason = ReasonInvalid
	}
	return &Error{Reason: reason, Msg: prefix + ge.Msg}
}
