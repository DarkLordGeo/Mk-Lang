// Why: a git server and its packs are untrusted input; every failure must
// come back as a value the caller can classify, never a panic.
//
// What: Error, its Reason constants (protocol, network, corrupt, limit,
// refused, not found), errorf, corrupt and clean.
//
// How: every fault of the wire, the pack or the tree is an *Error with a
// Reason, so a hostile server or pack cannot crash mk; internal/fetch maps
// each Reason to an E0202 reason.

// Package git is a client of git's smart HTTP protocol version 2 and a
// reader of its pack format: discovery, ls-refs, a depth-1 fetch, pack
// parsing with offset and reference deltas, every object id recomputed,
// and the walk of a commit's tree into regular files. It knows nothing
// about Mk; internal/fetch turns its files into a fetch.Tree.
package git

import (
	"fmt"
	"strconv"
)

// Reasons of an Error. internal/fetch maps each to an E0202 reason.
const (
	// ReasonProtocol: the server does not speak protocol version 2 over
	// smart HTTP, or its repository is not SHA-1.
	ReasonProtocol = "protocol"
	// ReasonNetwork: the request failed, or the server answered an HTTP
	// status other than 200, or sent an error on side band 3 or an ERR
	// line.
	ReasonNetwork = "network"
	// ReasonCorrupt: malformed pkt-lines, pack, object, delta or tree, a
	// bad trailer, or an object id that does not match its content.
	ReasonCorrupt = "corrupt"
	// ReasonLimit: one of the Limits was exceeded.
	ReasonLimit = "limit"
	// ReasonRefused: a tree entry git can hold and mk does not unpack: a
	// link (120000), a submodule (160000), an unknown mode, or a name
	// such as ".git".
	ReasonRefused = "refused"
	// ReasonNotFound: no ref of the name asked for.
	ReasonNotFound = "not-found"
	// ReasonInvalid: a URL the client will not use (not https outside
	// loopback, or holding credentials).
	ReasonInvalid = "invalid"
)

// Error is a failure of the git client. Msg names no credential.
type Error struct {
	Reason string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func errorf(reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Msg: fmt.Sprintf(format, args...)}
}

func corrupt(format string, args ...any) *Error {
	return errorf(ReasonCorrupt, format, args...)
}

// maxQuoted caps a server-derived string in a message.
const maxQuoted = 200

// clean quotes a server-derived string for a message: at most 200 bytes,
// with control characters (an escape sequence, a newline) escaped.
func clean(s string) string {
	if len(s) > maxQuoted {
		return strconv.Quote(s[:maxQuoted]) + "..."
	}
	return strconv.Quote(s)
}
