// Why: the Swiss table in coll_table.mk reads control bytes in 8-byte groups
// and reaches slot fields through raw pointers; this file holds that raw
// memory access so the table algorithm needs no unsafe block.
//
// What: CtrlAt, CtrlPut, GroupAt, KeyAt, ValueAt, NewCtrl and Discard.
//
// How: the control array has cap + 8 bytes, the first 8 mirrored at the end,
// and CtrlPut writes both copies, so a group read at any slot never wraps.
// GroupAt is one align 1 load of a little-endian word.

import std.raw

// CtrlAt is ctrl[i].
CtrlAt(ctrl: *u8, i: int): u8 {
    unsafe "the caller keeps i below cap + 8, the length of ctrl" {
        return raw.Read(raw.Offset(ctrl, i))
    }
}

// CtrlPut writes ctrl[i] = b and, when i < 8, its mirror ctrl[cap + i], so
// a group read never wraps and the mirror is always exact.
CtrlPut(ctrl: *u8, cap: int, i: int, b: u8) {
    unsafe "the caller keeps i below cap; ctrl holds cap + 8 bytes" {
        raw.Write(raw.Offset(ctrl, i), b)
        if i < 8 {
            raw.Write(raw.Offset(ctrl, cap + i), b)
        }
    }
}

// GroupAt is the 8 control bytes at pos, byte j of the word being ctrl[pos
// + j]: an align 1 load, since pos is any slot.
GroupAt(ctrl: *u8, pos: int): u64 {
    unsafe "pos < cap, and ctrl holds cap + 8 bytes" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(ctrl, pos)))
    }
}

// KeyAt and ValueAt are the fields of slot i.
KeyAt[K, V](slots: *Slot[K, V], i: int): *K {
    unsafe "field addresses; nothing is dereferenced" {
        return raw.Of(&(*raw.Offset(slots, i)).key)
    }
}

ValueAt[K, V](slots: *Slot[K, V], i: int): *V {
    unsafe "field addresses; nothing is dereferenced" {
        return raw.Of(&(*raw.Offset(slots, i)).value)
    }
}

// NewCtrl allocates cap + 8 control bytes, every one empty; null when a is
// out of memory.
NewCtrl(a: &Alloc, cap: int): *u8 {
    p := AllocBytes(a, cap + 8, 8)
    if raw.IsNull(p) {
        return p
    }
    unsafe "p is fresh memory for cap + 8 bytes" {
        raw.Fill(p, 0xFF, cap + 8)
    }
    return p
}

// Discard drops a plain value, or a linear one the caller has decided to
// drop after its check.
Discard[T](x: T) {
    unsafe "the caller checked raw.IsLinear" {
        raw.Forget(x)
    }
}
