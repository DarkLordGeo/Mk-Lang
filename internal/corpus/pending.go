// Why: a design block whose stage is not wired yet may be listed as pending
// on purpose, and the list must not go stale.
//
// What: PendingEntry and the reader of internal/corpus/pending.txt, one
// "document:line reason" per line, sorted.
//
// How: the file is empty: once every stage is wired, gate_test.go refuses
// any line, and a stale line fails TestPendingAllowlistIsCurrent.

package corpus

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
)

//go:embed pending.txt
var pendingTxt string

// PendingEntry is one allowlist line: a unit key and why it is pending.
type PendingEntry struct {
	Key    string // "doc/reference.md:17" or a conformance unit path
	Reason string
}

// Allowlist is the parsed pending.txt: entries in strictly increasing byte
// order of Key.
type Allowlist struct {
	Entries []PendingEntry
}

// Reason returns the allowlist reason for key and whether it is listed.
func (a Allowlist) Reason(key string) (string, bool) {
	i, ok := slices.BinarySearchFunc(a.Entries, key, func(e PendingEntry, k string) int { return strings.Compare(e.Key, k) })
	if !ok {
		return "", false
	}
	return a.Entries[i].Reason, true
}

// LoadPending parses the embedded internal/corpus/pending.txt.
func LoadPending() (Allowlist, error) { return ParseAllowlist(pendingTxt) }

// ParseAllowlist reads allowlist text: blank lines are ignored; every other
// line is a key, one space, and a non-empty reason; keys strictly increase.
func ParseAllowlist(text string) (Allowlist, error) {
	var a Allowlist
	for i, line := range splitLines(text) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, reason, ok := strings.Cut(line, " ")
		if !ok || key == "" || strings.TrimSpace(reason) == "" {
			return Allowlist{}, fmt.Errorf("pending.txt:%d: want \"document:line reason\", got %q", i+1, line)
		}
		if n := len(a.Entries); n > 0 && a.Entries[n-1].Key >= key {
			return Allowlist{}, fmt.Errorf("pending.txt:%d: %s is not after %s", i+1, key, a.Entries[n-1].Key)
		}
		a.Entries = append(a.Entries, PendingEntry{Key: key, Reason: strings.TrimSpace(reason)})
	}
	return a, nil
}
