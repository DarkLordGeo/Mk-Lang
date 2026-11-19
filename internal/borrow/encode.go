// Why: a warm build skips the borrow check of a body whose function key hit
// the cache, but later stages still need that body's borrow facts.
//
// What: EncodeFacts and DecodeFacts, borrow.Facts as deterministic bytes,
// which iface stores opaquely in a cached function result
// (FuncResult.Borrow).
//
// How: a version byte, then the onerr runs as uvarints in block order.
// DecodeFacts never panics and checks every run against the body it belongs
// to; any mismatch is ErrFactsCorrupt, which the caller treats as a miss.

package borrow

import (
	"encoding/binary"
	"errors"
	"fmt"

	"mk/internal/mir"
)

// factsVersion is the first byte of every encoding. A change of the format
// takes a new version; DecodeFacts refuses any other.
const factsVersion = 1

// ErrFactsCorrupt is wrapped by every DecodeFacts error: the bytes are not
// an encoding of Facts for that body (a torn or stale cache entry, never a
// user error). The caller treats the entry as a miss.
var ErrFactsCorrupt = errors.New("borrow: malformed facts")

// EncodeFacts writes f deterministically: equal Facts give equal bytes, and
// nothing in the bytes depends on IDs other than the body's own BlockIDs.
// Format: the version byte, the uvarint count of OnerrRuns, then per run its
// uvarint Block and one byte, 1 when Run. Runs are written in f's order,
// which Check gives as strictly increasing Block (report.go). A nil f encodes
// as no runs.
func EncodeFacts(f *Facts) []byte {
	var runs []OnerrRun
	if f != nil {
		runs = f.OnerrRuns
	}
	out := make([]byte, 0, 2+3*len(runs))
	out = append(out, factsVersion)
	out = binary.AppendUvarint(out, uint64(len(runs)))
	for _, r := range runs {
		out = binary.AppendUvarint(out, uint64(r.Block))
		if r.Run {
			out = append(out, 1)
		} else {
			out = append(out, 0)
		}
	}
	return out
}

// DecodeFacts reads what EncodeFacts wrote for body, the MIR body the facts
// belong to (the FuncResult's own Body). It never panics on any input. An
// error wraps ErrFactsCorrupt: a wrong version, truncated or trailing bytes,
// a non-minimal varint, a run byte other than 0 or 1, or a run whose Block
// is out of the body's range, not strictly after the previous run's, or not
// a TermOnerrGuard block. A nil body accepts only the encoding of no runs.
func DecodeFacts(b []byte, body *mir.Body) (Facts, error) {
	bad := func(format string, args ...any) (Facts, error) {
		return Facts{}, fmt.Errorf("%w: %s", ErrFactsCorrupt, fmt.Sprintf(format, args...))
	}
	if len(b) == 0 || b[0] != factsVersion {
		return bad("no version %d byte", factsVersion)
	}
	at := 1
	n, k := uvarint(b[at:])
	if k <= 0 {
		return bad("count at byte %d", at)
	}
	at += k
	// Each run takes at least two bytes, which bounds n before allocating.
	if n > uint64(len(b)-at)/2 {
		return bad("%d runs in %d bytes", n, len(b)-at)
	}
	numBlocks := 0
	if body != nil {
		numBlocks = len(body.Blocks)
	}
	var f Facts
	if n > 0 {
		f.OnerrRuns = make([]OnerrRun, 0, n)
	}
	for i := uint64(0); i < n; i++ {
		blk, k := uvarint(b[at:])
		if k <= 0 {
			return bad("block of run %d at byte %d", i, at)
		}
		at += k
		if blk >= uint64(numBlocks) {
			return bad("run %d names block %d of %d", i, blk, numBlocks)
		}
		if i > 0 && blk <= uint64(f.OnerrRuns[i-1].Block) {
			return bad("run %d names block %d after block %d", i, blk, f.OnerrRuns[i-1].Block)
		}
		if body.Blocks[blk].Term.Kind != mir.TermOnerrGuard {
			return bad("run %d names block %d, which is not an onerr guard", i, blk)
		}
		if at >= len(b) || b[at] > 1 {
			return bad("run byte of run %d at byte %d", i, at)
		}
		f.OnerrRuns = append(f.OnerrRuns, OnerrRun{Block: mir.BlockID(blk), Run: b[at] == 1})
		at++
	}
	if at != len(b) {
		return bad("%d trailing bytes", len(b)-at)
	}
	return f, nil
}

// uvarint is binary.Uvarint that also refuses a non-minimal encoding (k is 0
// then), so every accepted input is the one EncodeFacts writes.
func uvarint(b []byte) (uint64, int) {
	v, k := binary.Uvarint(b)
	if k > 1 && b[k-1] == 0 {
		return 0, 0
	}
	return v, k
}
