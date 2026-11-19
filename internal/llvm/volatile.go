// Why: std.mmio needs loads and stores the optimizer may not drop, merge or
// reorder.
//
// What: the volatile load and store builders.
//
// How: a volatile access carries none of the load facts (!range, !nonnull,
// !noundef), so these builders take no metadata.

package llvm

// LoadVolatile is load volatile t, ptr p, align a.
func (b *Block) LoadVolatile(t Type, p Value, align uint32) Value {
	b.f.m.checkType(t, "LoadVolatile")
	b.ptr(p, "LoadVolatile")
	v := b.beginValue(t, nil)
	b.str("load volatile ")
	b.typ(t)
	b.commaTyped(p)
	b.align(align)
	b.end(nil)
	return v
}

// StoreVolatile is store volatile v, ptr p, align a.
func (b *Block) StoreVolatile(v, p Value, align uint32) {
	b.val(v, "StoreVolatile")
	b.ptr(p, "StoreVolatile")
	b.begin(nil)
	b.str("store volatile ")
	b.typed(v)
	b.commaTyped(p)
	b.align(align)
	b.end(nil)
}
