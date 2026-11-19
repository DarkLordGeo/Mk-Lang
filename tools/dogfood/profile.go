// Why: cover learns which std functions a case executed from the profiles
// of --pgo-gen builds, and tools/dogfood may not import internal/pgo, so it
// carries its own reader of the profile format.
//
// What: readProfile (a version-1 .mkprof file into Records), FuncID,
// funcOfItem (the function a library line declares) and funcsOfKey (the
// std functions a symbol may instrument).
//
// How: a record's key is its function's symbol and Counts[0] its entry
// count; funcsOfKey strips "mk.", a generic instance's hash and an impl's
// wrapper to reach the module, receiver and name. Merging stays mk profile
// merge's.

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// profMagic opens every version-1 profile.
const profMagic = "MKPROF\x00\x01"

// Record is the part of a profile record cover needs.
type Record struct {
	Key   string
	Entry uint64 // Counts[0]
}

// readProfile reads a .mkprof file's records.
func readProfile(b []byte) ([]Record, error) {
	if len(b) < 16 || string(b[:8]) != profMagic {
		return nil, errors.New("not a version-1 .mkprof (bad magic)")
	}
	n := binary.LittleEndian.Uint64(b[8:])
	b = b[16:]
	if n > 1<<24 {
		return nil, fmt.Errorf("record count %d over 2^24", n)
	}
	out := make([]Record, 0, n)
	for i := uint64(0); i < n; i++ {
		if len(b) < 4 {
			return nil, fmt.Errorf("record %d truncated", i)
		}
		kl := int(binary.LittleEndian.Uint32(b))
		if kl > 64<<10 || len(b) < 4+kl+16+4 {
			return nil, fmt.Errorf("record %d truncated or its key over 64 KiB", i)
		}
		key := string(b[4 : 4+kl])
		b = b[4+kl+16:]
		c := int(binary.LittleEndian.Uint32(b))
		b = b[4:]
		if c > 1<<24 || len(b) < 8*c {
			return nil, fmt.Errorf("record %d (%s): counts truncated", i, key)
		}
		var entry uint64
		if c > 0 {
			entry = binary.LittleEndian.Uint64(b)
		}
		b = b[8*c:]
		out = append(out, Record{Key: key, Entry: entry})
	}
	if len(b) != 0 {
		return nil, fmt.Errorf("%d bytes after the last record", len(b))
	}
	return out, nil
}

// FuncID names a std function as the library surface does: its module, its
// receiver type ("" for a module's free function) and its name.
type FuncID struct{ Mod, Recv, Name string }

func (f FuncID) String() string {
	if f.Recv == "" {
		return f.Mod + "." + f.Name
	}
	return f.Mod + "." + f.Recv + ":" + f.Name
}

// funcOfItem is the function a library line declares: module free
// functions (fs.Rename, math.Max) have no receiver; false for a line that
// declares no function.
func funcOfItem(it *Item) (FuncID, bool) {
	if !it.Executable() {
		return FuncID{}, false
	}
	recv, name := itemHead(it.Text)
	if recv == modShort(it.Mod) || (it.InSect != "" && recv == modShort(it.InSect)) {
		recv = ""
	}
	return FuncID{Mod: it.Mod, Recv: recv, Name: name}, true
}

// funcsOfKey reads a symbol (as the build writes it) as the std functions it
// may instrument: "mk.std.hash.Crc32", "mk.std.core.str:Trim",
// "mk.std.math.Max.<16 hex>" (a generic instance), and
// "mk.std.core#impl(std.core.Slice for std.core.List):ItemsMut.<hex>" (an
// impl method: the interface's and the type's). Symbols outside std, fn
// literals and tests give none.
func funcsOfKey(key string) []FuncID {
	k, ok := strings.CutPrefix(key, "mk.")
	if !ok || !strings.HasPrefix(k, "std.") {
		return nil
	}
	k = stripInstance(k)
	if mod, rest, ok := strings.Cut(k, "#impl("); ok {
		inner, name, ok := strings.Cut(rest, "):")
		if !ok || strings.ContainsAny(name, ".#:") {
			return nil
		}
		iface, typ, ok := strings.Cut(inner, " for ")
		if !ok {
			return nil
		}
		var out []FuncID
		for _, t := range []string{iface, typ} {
			if i := strings.LastIndexByte(t, '.'); i >= 0 {
				out = append(out, FuncID{Mod: t[:i], Recv: t[i+1:], Name: name})
			} else {
				out = append(out, FuncID{Mod: mod, Recv: t, Name: name}) // int, str
			}
		}
		return out
	}
	if strings.Contains(k, "#") {
		return nil
	}
	if i := strings.LastIndexByte(k, ':'); i >= 0 {
		left, name := k[:i], k[i+1:]
		j := strings.LastIndexByte(left, '.')
		if j < 0 {
			return nil
		}
		return []FuncID{{Mod: left[:j], Recv: left[j+1:], Name: name}}
	}
	i := strings.LastIndexByte(k, '.')
	if i < 0 {
		return nil
	}
	return []FuncID{{Mod: k[:i], Name: k[i+1:]}}
}

// stripInstance drops a generic instance's ".<16 hex digits>" suffix.
func stripInstance(k string) string {
	i := strings.LastIndexByte(k, '.')
	if i < 0 || len(k)-i-1 != 16 {
		return k
	}
	for _, c := range k[i+1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return k
		}
	}
	return k[:i]
}
