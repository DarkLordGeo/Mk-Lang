// Why: profile-guided builds read counter profiles written by instrumented
// runs; the file format must be exact, canonical and safe to read from any
// input, because a profile's bytes are part of every release pack key.
//
// What: the .mkprof format (Magic, MaxKeyLen, MaxCount, HashDomain), Record
// and Profile, Read, Check, Bytes, Write, Hash, Lookup, HasKey and Equal.
//
// How: little-endian: the magic, a record count, then per record a key
// length and key, a 16-byte CFG checksum, a counter count and the counters.
// Records are sorted by (Key, Checksum) and unique, so equal profiles have
// equal bytes; Read rejects any other input with an error, never a panic.

// Package pgo reads, writes and merges .mkprof profiles, matches their records
// to functions by stable key and CFG checksum, and computes the hot and cold
// thresholds and branch weights codegen gives LLVM.
// It does not instrument code (codegen) and does not write profiles at run
// time (std/rt).
package pgo

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"mk/internal/source"
)

// Format constants of the .mkprof format, version 1.
const (
	// Magic opens every version-1 profile.
	Magic = "MKPROF\x00\x01"
	// MaxKeyLen is the longest key Read accepts and Write emits (64 KiB).
	MaxKeyLen = 64 << 10
	// MaxCount bounds both count-like fields of the format: the record count and a
	// record's counter count n (2^24 each).
	MaxCount = 1 << 24
	// HashDomain is the source.Hasher domain of Profile.Hash.
	HashDomain = "mk.pgo.v1"
)

// Record is one function's counters: Key is the function's symbol (stable
// across builds), Checksum the first 16 bytes of the instance body's mir
// CFGChecksum, Counts its counters in codegen's counter order: Counts[0] is
// the entry count.
type Record struct {
	Key      string
	Checksum source.Hash128
	Counts   []uint64
}

// Profile is a set of records sorted by (Key, Checksum), bytewise, and unique
// on that pair. Read and Merge produce only such profiles; Write refuses any
// other. A Profile is not modified by any method and may be shared. A nil
// *Profile is the empty profile for every method.
type Profile struct {
	Records []Record
}

// records is p's records, none for a nil p.
func (p *Profile) records() []Record {
	if p == nil {
		return nil
	}
	return p.Records
}

// less orders records by (key bytes, checksum bytes), the file's order.
func less(a, b *Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Checksum.Compare(b.Checksum) < 0
}

// cmpKey compares a record with (key, sum) in the order of less.
func cmpKey(r *Record, key string, sum source.Hash128) int {
	switch {
	case r.Key < key:
		return -1
	case r.Key > key:
		return 1
	}
	return r.Checksum.Compare(sum)
}

// Read decodes a version-1 profile. Any malformed input is an error,
// never a panic: a bad magic, a truncated header or record, a record count or
// counter count over MaxCount, a key over MaxKeyLen or not UTF-8, records out
// of order or duplicated, and bytes after the last record. Header values never
// size an allocation before the bytes they announce have been read.
func Read(r io.Reader) (*Profile, error) {
	br := bufio.NewReader(r)
	var magic [len(Magic)]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return nil, readErr("magic", err)
	}
	if string(magic[:]) != Magic {
		return nil, errors.New("pgo: not a version-1 .mkprof file (bad magic)")
	}
	var w8 [8]byte
	if _, err := io.ReadFull(br, w8[:]); err != nil {
		return nil, readErr("record count", err)
	}
	count := binary.LittleEndian.Uint64(w8[:])
	if count > MaxCount {
		return nil, fmt.Errorf("pgo: record count %d is over the limit %d", count, MaxCount)
	}
	p := &Profile{}
	var buf [4096]byte
	for i := uint64(0); i < count; i++ {
		var w4 [4]byte
		if _, err := io.ReadFull(br, w4[:]); err != nil {
			return nil, recErr(i, "key length", err)
		}
		keyLen := binary.LittleEndian.Uint32(w4[:])
		if keyLen > MaxKeyLen {
			return nil, fmt.Errorf("pgo: record %d: key length %d is over the limit %d", i, keyLen, MaxKeyLen)
		}
		key := make([]byte, keyLen) // bounded by MaxKeyLen
		if _, err := io.ReadFull(br, key); err != nil {
			return nil, recErr(i, "key", err)
		}
		if !utf8.Valid(key) {
			return nil, fmt.Errorf("pgo: record %d: key is not UTF-8", i)
		}
		rec := Record{Key: string(key)}
		if _, err := io.ReadFull(br, rec.Checksum[:]); err != nil {
			return nil, recErr(i, "checksum", err)
		}
		if _, err := io.ReadFull(br, w4[:]); err != nil {
			return nil, recErr(i, "counter count", err)
		}
		n := binary.LittleEndian.Uint32(w4[:])
		if n > MaxCount {
			return nil, fmt.Errorf("pgo: record %d (%q): counter count %d is over the limit %d", i, rec.Key, n, MaxCount)
		}
		// Read the counters in bounded chunks, so a large n on a short input
		// fails before it allocates.
		rec.Counts = make([]uint64, 0, min(int(n), len(buf)/8))
		for left := int(n); left > 0; {
			k := min(left, len(buf)/8)
			if _, err := io.ReadFull(br, buf[:k*8]); err != nil {
				return nil, recErr(i, "counters", err)
			}
			for j := 0; j < k; j++ {
				rec.Counts = append(rec.Counts, binary.LittleEndian.Uint64(buf[j*8:]))
			}
			left -= k
		}
		if len(p.Records) > 0 {
			prev := &p.Records[len(p.Records)-1]
			if !less(prev, &rec) {
				if prev.Key == rec.Key && prev.Checksum == rec.Checksum {
					return nil, fmt.Errorf("pgo: record %d: duplicate record %q checksum %s", i, rec.Key, rec.Checksum)
				}
				return nil, fmt.Errorf("pgo: record %d: %q checksum %s is out of order after %q checksum %s",
					i, rec.Key, rec.Checksum, prev.Key, prev.Checksum)
			}
		}
		p.Records = append(p.Records, rec)
	}
	if _, err := br.ReadByte(); err == nil {
		return nil, fmt.Errorf("pgo: bytes after the last of %d records", count)
	} else if err != io.EOF {
		return nil, fmt.Errorf("pgo: reading past the last record: %w", err)
	}
	return p, nil
}

func readErr(what string, err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return fmt.Errorf("pgo: truncated profile: missing %s", what)
	}
	return fmt.Errorf("pgo: reading %s: %w", what, err)
}

func recErr(i uint64, what string, err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return fmt.Errorf("pgo: truncated profile: record %d: missing %s", i, what)
	}
	return fmt.Errorf("pgo: record %d: reading %s: %w", i, what, err)
}

// Check reports whether p is canonical: records sorted by (Key, Checksum) and
// unique, at most MaxCount of them, each key UTF-8 and at most MaxKeyLen
// bytes, each counter count at most MaxCount. Exactly the canonical profiles
// are the ones Write accepts and Read returns.
func (p *Profile) Check() error {
	recs := p.records()
	if uint64(len(recs)) > MaxCount {
		return fmt.Errorf("pgo: %d records is over the limit %d", len(recs), MaxCount)
	}
	for i := range recs {
		r := &recs[i]
		if len(r.Key) > MaxKeyLen {
			return fmt.Errorf("pgo: record %d: key length %d is over the limit %d", i, len(r.Key), MaxKeyLen)
		}
		if !utf8.ValidString(r.Key) {
			return fmt.Errorf("pgo: record %d: key is not UTF-8", i)
		}
		if len(r.Counts) > MaxCount {
			return fmt.Errorf("pgo: record %d (%q): counter count %d is over the limit %d", i, r.Key, len(r.Counts), MaxCount)
		}
		if i > 0 && !less(&recs[i-1], r) {
			return fmt.Errorf("pgo: record %d: %q checksum %s is out of order or duplicated", i, r.Key, r.Checksum)
		}
	}
	return nil
}

// encode appends p's records in their order in the file layout. On a canonical
// profile the result is the canonical bytes.
func (p *Profile) encode(b []byte) []byte {
	b = append(b, Magic...)
	recs := p.records()
	b = binary.LittleEndian.AppendUint64(b, uint64(len(recs)))
	for i := range recs {
		r := &recs[i]
		b = binary.LittleEndian.AppendUint32(b, uint32(len(r.Key)))
		b = append(b, r.Key...)
		b = append(b, r.Checksum[:]...)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(r.Counts)))
		for _, c := range r.Counts {
			b = binary.LittleEndian.AppendUint64(b, c)
		}
	}
	return b
}

// size is the encoded length of p.
func (p *Profile) size() int {
	n := len(Magic) + 8
	for _, r := range p.records() {
		n += 4 + len(r.Key) + 16 + 4 + 8*len(r.Counts)
	}
	return n
}

// Bytes returns p's canonical bytes, or the error of Check.
func (p *Profile) Bytes() ([]byte, error) {
	if err := p.Check(); err != nil {
		return nil, err
	}
	return p.encode(make([]byte, 0, p.size())), nil
}

// Write writes p's canonical bytes: equal profiles give equal bytes, and
// Read of the bytes returns an equal profile. A profile that is not canonical
// (see Check) is an error and nothing is written.
func (p *Profile) Write(w io.Writer) error {
	b, err := p.Bytes()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// Hash is SHA-256, in the source.Hasher domain HashDomain ("mk.pgo.v1"), of
// one Bytes write of p's canonical bytes. It is the profile's part of every
// release object-pack key. On a profile that is not canonical it hashes the
// records in their given order, so it is still deterministic and never
// panics.
func (p *Profile) Hash() source.Hash {
	return source.HashBytes(HashDomain, p.encode(make([]byte, 0, p.size())))
}

// Lookup returns the counts recorded for key with exactly this checksum and
// exactly n counters (n is codegen's counter count for the function).
// A record with the same key and another checksum, or with len(Counts) != n,
// is stale: Lookup returns false and the caller lists the function in
// Report.Stale. The returned slice has length and capacity n, so no caller
// indexes counts past n; it aliases the profile and must not be modified.
// p must be canonical (sorted), as Read and Merge return it.
func (p *Profile) Lookup(key string, sum source.Hash128, n int) ([]uint64, bool) {
	recs := p.records()
	i := sort.Search(len(recs), func(i int) bool { return cmpKey(&recs[i], key, sum) >= 0 })
	if i == len(recs) || cmpKey(&recs[i], key, sum) != 0 {
		return nil, false
	}
	c := recs[i].Counts
	if n < 0 || len(c) != n {
		return nil, false
	}
	return c[:n:n], true
}

// HasKey reports whether p holds a record for key under any checksum. With
// Lookup it tells a stale function (key present, Lookup false) from one the
// profile never saw (key absent), so codegen lists only the first in
// Report.Stale. p must be canonical, as for Lookup.
func (p *Profile) HasKey(key string) bool {
	recs := p.records()
	i := sort.Search(len(recs), func(i int) bool { return recs[i].Key >= key })
	return i < len(recs) && recs[i].Key == key
}

// Equal reports whether p and q hold the same records in the same order.
func (p *Profile) Equal(q *Profile) bool {
	pr, qr := p.records(), q.records()
	if len(pr) != len(qr) {
		return false
	}
	for i := range pr {
		a, b := &pr[i], &qr[i]
		if a.Key != b.Key || a.Checksum != b.Checksum || len(a.Counts) != len(b.Counts) {
			return false
		}
		for j := range a.Counts {
			if a.Counts[j] != b.Counts[j] {
				return false
			}
		}
	}
	return true
}
