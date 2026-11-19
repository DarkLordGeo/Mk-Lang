// Why: a fetch reply is a pack file from an untrusted server; reading it
// must bound time and memory and check every byte.
//
// What: ParsePack, Limits (with their defaults), and Store, the parsed
// objects by id.
//
// How: a pack is "PACK", version 2 or 3, an object count, the
// objects, and the SHA-1 of everything before it, which is checked. Each
// object is a type and size varint, a base (an offset varint for an
// offset delta, a 20-byte id for a reference delta), then a zlib stream
// whose inflated length must equal the declared size. Every object's id
// is recomputed from its content, delta results included. The limits are
// reason values, never panics; nothing is allocated from a declared count
// or size.

package git

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"io"
)

// Limits bound what a pack and the tree walked from it may hold. The zero
// value of a field means its default.
type Limits struct {
	MaxObjects    int   // 0: DefaultMaxObjects
	MaxBytes      int64 // 0: DefaultMaxBytes; inflated objects plus delta results, and the walk's file bytes
	MaxDeltaChain int   // 0: DefaultMaxDeltaChain
	MaxDepth      int   // 0: DefaultMaxDepth; nested directories below the root tree
	MaxFiles      int   // 0: DefaultMaxFiles; tree entries (files and directories) the walk visits
}

// The default limits.
const (
	DefaultMaxObjects    = 1_000_000
	DefaultMaxBytes      = 1 << 30 // fetch.DefaultMaxArchive
	DefaultMaxDeltaChain = 4095    // git's own maximum
	DefaultMaxDepth      = 256
	DefaultMaxFiles      = 1_000_000
)

func (l Limits) withDefaults() Limits {
	if l.MaxObjects <= 0 {
		l.MaxObjects = DefaultMaxObjects
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxDeltaChain <= 0 {
		l.MaxDeltaChain = DefaultMaxDeltaChain
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	return l
}

// Store holds the objects of a parsed pack by their recomputed ids.
type Store struct {
	objs map[OID]Object
}

// Get returns the object whose id is o.
func (s *Store) Get(o OID) (Object, bool) {
	obj, ok := s.objs[o]
	return obj, ok
}

// Len is the number of distinct objects.
func (s *Store) Len() int { return len(s.objs) }

// packEntry is one object as read, before its delta is resolved.
type packEntry struct {
	off     int
	typ     ObjectType // the final type once resolved
	data    []byte     // the content, or the delta instructions
	baseOff int        // an offset delta's base, as an offset
	baseOID OID        // a reference delta's base
	delta   bool
	done    bool
	depth   int
	oid     OID
}

// ParsePack reads a whole pack and resolves its deltas.
func ParsePack(data []byte, lim Limits) (*Store, error) {
	lim = lim.withDefaults()
	if len(data) < 12+20 {
		return nil, corrupt("the pack is %d bytes, shorter than a header and a trailer", len(data))
	}
	if string(data[:4]) != "PACK" {
		return nil, corrupt("the pack does not start with PACK")
	}
	if v := binary.BigEndian.Uint32(data[4:8]); v != 2 && v != 3 {
		return nil, corrupt("the pack has version %d; versions 2 and 3 are read", v)
	}
	count := binary.BigEndian.Uint32(data[8:12])
	if uint64(count) > uint64(lim.MaxObjects) {
		return nil, errorf(ReasonLimit, "the pack holds %d objects, over the limit of %d", count, lim.MaxObjects)
	}
	end := len(data) - 20
	if sum := sha1.Sum(data[:end]); !bytes.Equal(sum[:], data[end:]) {
		return nil, corrupt("the pack's trailer is not the SHA-1 of its contents")
	}

	p := &packParser{data: data[:end], budget: lim.MaxBytes, byOff: map[int]int{}}
	pos := 12
	for i := uint32(0); i < count; i++ {
		if pos >= end {
			return nil, corrupt("the pack ends after %d of its %d objects", i, count)
		}
		next, err := p.entry(pos)
		if err != nil {
			return nil, err
		}
		pos = next
	}
	if pos != end {
		return nil, corrupt("the pack holds %d bytes after its last object", end-pos)
	}
	return p.resolve(lim)
}

// inflateChunk is the first capacity of an object's inflate buffer.
const inflateChunk = 64 << 10

// maxDeflateRatio bounds how far deflate expands its input (RFC 1951's
// longest match per bit: about 1,032 to 1).
const maxDeflateRatio = 1032

type packParser struct {
	data    []byte
	budget  int64 // inflated bytes left
	entries []packEntry
	byOff   map[int]int // offset -> index in entries
	zr      io.ReadCloser
}

// entry reads the object at pos and returns where the next one starts.
func (p *packParser) entry(pos int) (int, error) {
	start := pos
	c := p.data[pos]
	pos++
	typ := ObjectType(c >> 4 & 7)
	size := uint64(c & 0x0f)
	for shift := uint(4); c&0x80 != 0; shift += 7 {
		if pos >= len(p.data) || shift > 60 {
			return 0, corrupt("the object at offset %d has a malformed size", start)
		}
		c = p.data[pos]
		pos++
		if uint64(c&0x7f)<<shift>>shift != uint64(c&0x7f) {
			return 0, corrupt("the object at offset %d has a size over 2^64", start)
		}
		size |= uint64(c&0x7f) << shift
	}
	e := packEntry{off: start, typ: typ}
	switch typ {
	case ObjCommit, ObjTree, ObjBlob, ObjTag:
	case objOfsDelta:
		// git's offset encoding: each continuation adds one before the shift.
		if pos >= len(p.data) {
			return 0, corrupt("the offset delta at %d is cut short", start)
		}
		c = p.data[pos]
		pos++
		back := uint64(c & 0x7f)
		for c&0x80 != 0 {
			if pos >= len(p.data) || back > 1<<56 {
				return 0, corrupt("the offset delta at %d has a malformed base offset", start)
			}
			c = p.data[pos]
			pos++
			back = (back+1)<<7 | uint64(c&0x7f)
		}
		if back == 0 || back > uint64(start) {
			return 0, corrupt("the offset delta at %d names a base %d bytes before it", start, back)
		}
		e.baseOff = start - int(back)
		e.delta = true
	case objRefDelta:
		if pos+20 > len(p.data) {
			return 0, corrupt("the reference delta at %d is cut short", start)
		}
		copy(e.baseOID[:], p.data[pos:pos+20])
		pos += 20
		e.delta = true
	default:
		return 0, corrupt("the object at offset %d has %s, which no pack holds", start, typ)
	}
	if size > uint64(p.budget) {
		return 0, errorf(ReasonLimit, "the pack inflates to more than its limit of bytes (an object of %d bytes at offset %d)", size, start)
	}
	body, n, err := p.inflate(pos, int64(size))
	if err != nil {
		return 0, err
	}
	if int64(len(body)) != int64(size) {
		return 0, corrupt("the object at offset %d inflates to %d bytes, but declares %d", start, len(body), size)
	}
	p.budget -= int64(size)
	e.data = body
	p.byOff[start] = len(p.entries)
	p.entries = append(p.entries, e)
	return pos + n, nil
}

// inflate reads the zlib stream at pos, at most size+1 bytes of output,
// and returns the output and the compressed length. The stream must end
// (its checksum read): a bytes.Reader is an io.ByteReader, so flate reads
// no byte past the stream.
func (p *packParser) inflate(pos int, size int64) ([]byte, int, error) {
	br := bytes.NewReader(p.data[pos:])
	var err error
	if p.zr == nil {
		p.zr, err = zlib.NewReader(br)
	} else {
		err = p.zr.(zlib.Resetter).Reset(br, nil)
	}
	if err != nil {
		return nil, 0, corrupt("the object at offset %d is not a zlib stream: %v", pos, err)
	}
	// The buffer starts at the declared size+1 only as far as deflate can
	// expand the compressed bytes left (at most 1,032 to 1), so three bytes
	// declaring 1 GiB allocate 3 KiB; it then doubles up to size+1.
	left := int64(len(p.data) - pos)
	buf := make([]byte, 0, min(size+1, max(inflateChunk, maxDeflateRatio*left)))
	for {
		if int64(len(buf)) > size {
			return nil, 0, corrupt("the object at offset %d inflates to more than its declared %d bytes", pos, size)
		}
		if len(buf) == cap(buf) {
			// Double, but never past size+1: the declared size is
			// allocated only once half of it has arrived.
			nb := make([]byte, len(buf), min(2*int64(cap(buf)), size+1))
			copy(nb, buf)
			buf = nb
		}
		want := min(int64(cap(buf)-len(buf)), size+1-int64(len(buf)))
		n, err := p.zr.Read(buf[len(buf) : len(buf)+int(want)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, corrupt("the object at offset %d does not inflate: %v", pos, err)
		}
	}
	if int64(len(buf)) > size {
		return nil, 0, corrupt("the object at offset %d inflates to more than its declared %d bytes", pos, size)
	}
	// The loop stopped at io.EOF from the zlib reader itself, after its
	// checksum.
	return buf, len(p.data) - pos - br.Len(), nil
}

// resolve applies every delta, breadth first from the whole objects, so
// a chain costs one application per link and no recursion.
func (p *packParser) resolve(lim Limits) (*Store, error) {
	st := &Store{objs: make(map[OID]Object, len(p.entries))}
	ofsKids := map[int][]int{} // base index -> offset deltas on it
	refKids := map[OID][]int{} // base id -> reference deltas on it
	var queue []int
	for i := range p.entries {
		e := &p.entries[i]
		if !e.delta {
			e.oid = HashObject(e.typ, e.data)
			e.done = true
			queue = append(queue, i)
			continue
		}
		if e.typ == objOfsDelta {
			b, ok := p.byOff[e.baseOff]
			if !ok {
				return nil, corrupt("the offset delta at %d names %d, where no object starts", e.off, e.baseOff)
			}
			ofsKids[b] = append(ofsKids[b], i)
		} else {
			refKids[e.baseOID] = append(refKids[e.baseOID], i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		b := &p.entries[i]
		if _, ok := st.objs[b.oid]; !ok {
			st.objs[b.oid] = Object{Type: b.typ, Data: b.data}
		}
		kids := ofsKids[i]
		delete(ofsKids, i)
		kids = append(kids, refKids[b.oid]...)
		delete(refKids, b.oid)
		for _, k := range kids {
			e := &p.entries[k]
			if e.done {
				continue
			}
			if b.depth+1 > lim.MaxDeltaChain {
				return nil, errorf(ReasonLimit, "the pack has a delta chain longer than %d", lim.MaxDeltaChain)
			}
			out, err := ApplyDelta(b.data, e.data, p.budget)
			if err != nil {
				return nil, err
			}
			p.budget -= int64(len(out))
			e.typ, e.data, e.depth, e.done = b.typ, out, b.depth+1, true
			e.oid = HashObject(e.typ, e.data)
			queue = append(queue, k)
		}
	}
	for i := range p.entries {
		if e := &p.entries[i]; !e.done {
			if e.typ == objRefDelta {
				return nil, corrupt("the reference delta at %d names base %s, which is not in the pack", e.off, e.baseOID)
			}
			return nil, corrupt("the delta at %d has no base it can be resolved from", e.off)
		}
	}
	return st, nil
}
