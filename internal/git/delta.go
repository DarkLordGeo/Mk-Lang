// Why: git packs store most objects as deltas against other objects; a pack
// cannot be read without applying them, and a hostile delta must not
// allocate or crash.
//
// What: ApplyDelta and deltaVarint.
//
// How: git's gitformat-pack "Deltified representation": a delta holds the base's
// size and the result's size as little-endian base-128 varints, then
// instructions, each a copy from the base (high bit set, offset and size
// bytes chosen by the low seven bits, a size of 0 meaning 0x10000) or an
// insert of 1 to 127 literal bytes. Instruction 0 is reserved and refused.

package git

// ApplyDelta applies delta to base. The result may be at most maxSize
// bytes (the inflated-data budget); a larger declared size is
// ReasonLimit. Every other fault is ReasonCorrupt. Nothing is allocated
// from a declared size: the result grows as instructions produce it.
func ApplyDelta(base, delta []byte, maxSize int64) ([]byte, error) {
	pos := 0
	srcSize, ok := deltaVarint(delta, &pos)
	if !ok {
		return nil, corrupt("a delta's base size is malformed")
	}
	dstSize, ok := deltaVarint(delta, &pos)
	if !ok {
		return nil, corrupt("a delta's result size is malformed")
	}
	if srcSize != uint64(len(base)) {
		return nil, corrupt("a delta expects a base of %d bytes, but its base has %d", srcSize, len(base))
	}
	if maxSize < 0 || dstSize > uint64(maxSize) {
		return nil, errorf(ReasonLimit, "a delta result of %d bytes is over the pack's inflated-data limit", dstSize)
	}
	var out []byte
	for pos < len(delta) {
		c := delta[pos]
		pos++
		switch {
		case c&0x80 != 0:
			var off, size uint64
			for i := uint(0); i < 4; i++ {
				if c&(1<<i) != 0 {
					if pos >= len(delta) {
						return nil, corrupt("a delta copy instruction is cut short")
					}
					off |= uint64(delta[pos]) << (8 * i)
					pos++
				}
			}
			for i := uint(0); i < 3; i++ {
				if c&(0x10<<i) != 0 {
					if pos >= len(delta) {
						return nil, corrupt("a delta copy instruction is cut short")
					}
					size |= uint64(delta[pos]) << (8 * i)
					pos++
				}
			}
			if size == 0 {
				size = 0x10000
			}
			if off+size > uint64(len(base)) {
				return nil, corrupt("a delta copies bytes %d to %d of a base of %d", off, off+size, len(base))
			}
			if uint64(len(out))+size > dstSize {
				return nil, corrupt("a delta writes past its declared size %d", dstSize)
			}
			out = append(out, base[off:off+size]...)
		case c != 0:
			n := int(c)
			if pos+n > len(delta) {
				return nil, corrupt("a delta insert instruction is cut short")
			}
			if uint64(len(out)+n) > dstSize {
				return nil, corrupt("a delta writes past its declared size %d", dstSize)
			}
			out = append(out, delta[pos:pos+n]...)
			pos += n
		default:
			return nil, corrupt("a delta holds the reserved instruction 0")
		}
	}
	if uint64(len(out)) != dstSize {
		return nil, corrupt("a delta produced %d bytes, but declares %d", len(out), dstSize)
	}
	if out == nil {
		out = []byte{}
	}
	return out, nil
}

// deltaVarint reads a little-endian base-128 varint of at most 64 bits.
func deltaVarint(b []byte, pos *int) (uint64, bool) {
	var v uint64
	for shift := uint(0); ; shift += 7 {
		if *pos >= len(b) || shift > 63 {
			return 0, false
		}
		c := b[*pos]
		*pos++
		if shift == 63 && c > 1 {
			return 0, false
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, true
		}
	}
}
