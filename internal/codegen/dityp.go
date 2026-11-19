// Why: a debugger prints a value only through a type description that
// matches the bytes the program really has, and Mk's layouts (enums,
// optionals, error unions) are its own.
//
// What: the type half of full debug info: diType and makeDIType, with
// diPointer, diFnPointer, diComposite, diArray, diEnumeration, diTagged and
// diOpaque. Only with Config.Debug; the function half is debug_full.go.
//
// How: one DI type node per concrete Mk type a pack's variables and
// signatures name, memoized per pack and built from types.Layout, so every
// member sits at the offset the program uses.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/types"
)

// diType is t's debug type, memoized per pack. A composite is reserved and
// memoized before its members are made, so a type that holds a pointer to
// itself terminates. void and never are the zero MD (null).
func (g *packGen) diType(t types.TypeID) llvm.MD {
	if md, ok := g.dbgTypes[t]; ok {
		return md
	}
	md := g.makeDIType(t)
	g.dbgTypes[t] = md
	return md
}

func (g *packGen) typeName(t types.TypeID) string { return g.tt.TypeString(t, nil) }

func (g *packGen) makeDIType(t types.TypeID) llvm.MD {
	mt := g.m.Metadata()
	ty := g.tt.Get(t)
	switch ty.Kind {
	case types.KVoid, types.KNever:
		return llvm.MD{}
	case types.KBool:
		return mt.DIBasicType("bool", 8, "DW_ATE_boolean")
	case types.KChar:
		return mt.DIBasicType("char", 32, "DW_ATE_UTF")
	case types.KInt, types.KIntPtr:
		bits := uint64(ty.Bits)
		if ty.Kind == types.KIntPtr {
			bits = 64
		}
		enc := "DW_ATE_unsigned"
		if ty.Signed {
			enc = "DW_ATE_signed"
		}
		return mt.DIBasicType(g.typeName(t), bits, enc)
	case types.KFloat:
		return mt.DIBasicType(g.typeName(t), uint64(ty.Bits), "DW_ATE_float")
	case types.KRef, types.KMut, types.KRawPtr:
		return g.diPointer(t, ty.Elem)
	case types.KFn:
		return g.diFnPointer(t, ty.Fn)
	case types.KStr, types.KSlice, types.KMutSlice:
		elem := ty.Elem
		if ty.Kind == types.KStr {
			elem = types.U8
		}
		return g.diComposite(t, func(self llvm.MD) []llvm.MD {
			ptr := mt.DIDerived(llvm.DerivedType{Tag: "DW_TAG_pointer_type", Base: g.diType(elem), Size: 64})
			return []llvm.MD{
				g.diMember(self, "ptr", ptr, 64, 0),
				g.diMember(self, "len", g.diType(types.Int), 64, 64),
			}
		})
	case types.KOptional:
		if types.NicheOptional(g.tt.Kind(ty.Elem)) {
			// ?&T, ?mut T, ?fn: one pointer word, null for none.
			if et := g.tt.Get(ty.Elem); et.Kind == types.KFn {
				return g.diFnPointer(t, et.Fn)
			}
			return g.diPointer(t, g.tt.Get(ty.Elem).Elem)
		}
	}
	l := g.env.Layout(t)
	if l == nil || l.Shape.Size == 0 {
		return g.diOpaque(t, l)
	}
	switch ty.Kind {
	case types.KArray:
		return g.diArray(t, ty.Elem, l)
	case types.KRecord:
		return g.diComposite(t, func(self llvm.MD) []llvm.MD {
			var els []llvm.MD
			for i, f := range l.Fields {
				name := "_" + itoa(i)
				if i < len(ty.Fields) {
					name = g.tt.Interner().Str(ty.Fields[i].Name)
				}
				els = g.appendField(els, self, name, f)
			}
			return els
		})
	case types.KOptional:
		// {some: bool, value: T}: the some byte as a bool.
		return g.diTagged(t, l, []string{"none", "value"}, nil, "some", true)
	case types.KErrorUnion:
		return g.diTagged(t, l, nil, nil, "code", false)
	case types.KErrorSet:
		return g.diTagged(t, l, []string{}, nil, "code", false)
	case types.KNamed:
		switch d := g.items.Get(ty.Item).Decl.(type) {
		case *types.StructDecl:
			return g.diComposite(t, func(self llvm.MD) []llvm.MD {
				var els []llvm.MD
				for i, f := range l.Fields {
					name := "_" + itoa(i)
					if i < len(d.Fields) {
						name = g.tt.Interner().Str(d.Fields[i].Name)
					}
					els = g.appendField(els, self, name, f)
				}
				return els
			})
		case *types.EnumDecl:
			names := make([]string, len(d.Variants))
			for i, v := range d.Variants {
				names[i] = g.tt.Interner().Str(v.Name)
			}
			if r := g.reprOf(t); r.kind == rScalar && r.class == sTag {
				return g.diEnumeration(t, r, l, names)
			}
			// A payload enum: every variant with fields is a member structure
			// named by the variant (fields non-nil, even with no names).
			fields := make([][]string, len(d.Variants))
			for i, v := range d.Variants {
				for _, f := range v.Fields {
					fields[i] = append(fields[i], g.tt.Interner().Str(f.Name))
				}
			}
			return g.diTagged(t, l, names, fields, "tag", false)
		}
	}
	return g.diOpaque(t, l)
}

// diPointer is a pointer type named t to elem.
func (g *packGen) diPointer(t, elem types.TypeID) llvm.MD {
	return g.m.Metadata().DIDerived(llvm.DerivedType{Tag: "DW_TAG_pointer_type", Name: g.typeName(t),
		Base: g.diType(elem), Size: 64})
}

// diFnPointer is a pointer to the subroutine type of f.
func (g *packGen) diFnPointer(t types.TypeID, f *types.FnType) llvm.MD {
	mt := g.m.Metadata()
	var sub llvm.MD
	if f != nil {
		ps := make([]llvm.MD, len(f.Params))
		for i, p := range f.Params {
			ps[i] = g.diType(p.Type)
		}
		sub = mt.DISubroutineTypeOf(g.diType(f.Result), ps...)
	}
	return mt.DIDerived(llvm.DerivedType{Tag: "DW_TAG_pointer_type", Name: g.typeName(t), Base: sub, Size: 64})
}

// diMember is a member of scope at bit offset off with size bits.
func (g *packGen) diMember(scope llvm.MD, name string, ty llvm.MD, bits, off uint64) llvm.MD {
	return g.m.Metadata().DIDerived(llvm.DerivedType{Tag: "DW_TAG_member", Name: name, Scope: scope,
		Base: ty, Size: bits, Offset: off})
}

// appendField appends the member of field f (a zero-sized field has none).
func (g *packGen) appendField(els []llvm.MD, self llvm.MD, name string, f types.FieldLayout) []llvm.MD {
	r := g.reprOf(f.Type)
	if r.size == 0 {
		return els
	}
	return append(els, g.diMember(self, name, g.diType(f.Type), r.size*8, f.Offset*8))
}

// diComposite is the distinct structure type named t whose members members
// makes, given the type's own node as their scope.
func (g *packGen) diComposite(t types.TypeID, members func(self llvm.MD) []llvm.MD) llvm.MD {
	mt := g.m.Metadata()
	r := g.reprOf(t)
	self := mt.Reserve()
	g.dbgTypes[t] = self
	els := members(self)
	g.fillComposite(self, t, "DW_TAG_structure_type", r.size, r.align, llvm.MD{}, els)
	return self
}

// fillComposite fills a reserved composite of tag with its elements.
func (g *packGen) fillComposite(self llvm.MD, t types.TypeID, tag string, size uint64, align uint32, base llvm.MD, els []llvm.MD) {
	mt := g.m.Metadata()
	ops := make([]llvm.MDOp, len(els))
	for i, e := range els {
		ops[i] = llvm.MDRef(e)
	}
	mt.FillComposite(self, llvm.CompositeType{Tag: tag, Name: g.typeName(t), Base: base, Size: size * 8,
		Align: uint64(max(align, 1)) * 8, Elements: mt.Tuple(ops...), Identifier: g.diIdentifier(t)})
}

// diIdentifier is the ODR identifier of t: its stable type key, so LTO
// merges one type's copies from several packs (same key, same layout on
// one target).
func (g *packGen) diIdentifier(t types.TypeID) string {
	return "mk." + hex16(typeKey(g.tt, t))
}

// diOpaque is a structure of t's exact size with no members: the fallback
// for a type the emitter does not describe, never an ICE.
func (g *packGen) diOpaque(t types.TypeID, l *types.Layout) llvm.MD {
	var size uint64
	var align uint32 = 1
	if l != nil {
		size, align = l.Shape.Size, max(l.Shape.Align, 1)
	}
	self := g.m.Metadata().Reserve()
	g.dbgTypes[t] = self
	g.fillComposite(self, t, "DW_TAG_structure_type", size, align, llvm.MD{}, nil)
	return self
}

// diArray is [N]elem with N from the layout (size over element size).
func (g *packGen) diArray(t, elem types.TypeID, l *types.Layout) llvm.MD {
	mt := g.m.Metadata()
	self := mt.Reserve()
	g.dbgTypes[t] = self
	er := g.reprOf(elem)
	var n uint64
	if er.size > 0 {
		n = l.Shape.Size / er.size
	}
	g.fillComposite(self, t, "DW_TAG_array_type", l.Shape.Size, l.Shape.Align, g.diType(elem),
		[]llvm.MD{mt.DISubrange(int64(n))})
	return self
}

// diEnumeration is a payload-free enum: an enumeration over its tag.
func (g *packGen) diEnumeration(t types.TypeID, r *repr, l *types.Layout, names []string) llvm.MD {
	mt := g.m.Metadata()
	self := mt.Reserve()
	g.dbgTypes[t] = self
	enc := "DW_ATE_unsigned"
	if r.signed {
		enc = "DW_ATE_signed"
	}
	base := mt.DIBasicType("u"+itoa(int(r.size*8)), r.size*8, enc)
	if r.signed {
		base = mt.DIBasicType("i"+itoa(int(r.size*8)), r.size*8, enc)
	}
	var els []llvm.MD
	for i, v := range l.Variants {
		name := "_" + itoa(i)
		if i < len(names) {
			name = names[i]
		}
		d := v.Discr
		if r.signed {
			d = signExtend(d, int(r.size*8))
		}
		els = append(els, mt.DIEnumerator(name, d, !r.signed))
	}
	g.fillComposite(self, t, "DW_TAG_enumeration_type", r.size, r.align, base, els)
	return self
}

// diTagged is a tagged type ({tag, payload}: an enum with payloads, ?T,
// E!T) as a structure: the tag as an unsigned member named tagName (a bool
// when boolTag and the tag is one byte holding 0 or 1: ?T's some byte),
// then each variant with fields at its fields' offsets (members overlap,
// as the variants do). With fields (a payload enum) every such variant is
// a member structure named by the variant, carrying its fields (named by
// fields[variant], "_k" when unnamed) at offsets relative to the variant's
// first byte: {tag, Circle: struct {r: f64}}. Without (?T, E!T) a
// one-field variant is that field directly, named by the variant
// ({some: bool, value: T}, {code: u32, ok: T}).
func (g *packGen) diTagged(t types.TypeID, l *types.Layout, names []string, fields [][]string, tagName string, boolTag bool) llvm.MD {
	mt := g.m.Metadata()
	return g.diComposite(t, func(self llvm.MD) []llvm.MD {
		var els []llvm.MD
		if l.HasTag && !l.Tag.Niche && l.Tag.Size > 0 {
			bits := uint64(l.Tag.Size) * 8
			tag := mt.DIBasicType("u"+itoa(int(bits)), bits, "DW_ATE_unsigned")
			if boolTag && bits == 8 && discrsBool(l) {
				tag = g.diType(types.Bool)
			}
			els = append(els, g.diMember(self, tagName, tag, bits, l.Tag.Offset*8))
		}
		for i, v := range l.Variants {
			if len(v.Fields) == 0 {
				continue
			}
			name := "_" + itoa(i)
			switch {
			case i < len(names):
				name = names[i]
			case names == nil && i == 0:
				name = "ok"
			case names == nil:
				name = "err" + itoa(i)
			}
			if len(v.Fields) == 1 && fields == nil {
				els = g.appendField(els, self, name, v.Fields[0])
				continue
			}
			lo, hi := v.Fields[0].Offset, uint64(0)
			for _, f := range v.Fields {
				lo = min(lo, f.Offset)
				hi = max(hi, f.Offset+g.reprOf(f.Type).size)
			}
			vs := mt.Reserve()
			var fs []llvm.MD
			for k, f := range v.Fields {
				fname := "_" + itoa(k)
				if i < len(fields) && k < len(fields[i]) && fields[i][k] != "" {
					fname = fields[i][k]
				}
				if fr := g.reprOf(f.Type); fr.size > 0 {
					fs = append(fs, g.diMember(vs, fname, g.diType(f.Type), fr.size*8, (f.Offset-lo)*8))
				}
			}
			ops := make([]llvm.MDOp, len(fs))
			for k, f := range fs {
				ops[k] = llvm.MDRef(f)
			}
			mt.FillComposite(vs, llvm.CompositeType{Tag: "DW_TAG_structure_type", Name: name, Scope: self,
				Size: (hi - lo) * 8, Elements: mt.Tuple(ops...)})
			els = append(els, g.diMember(self, name, vs, (hi-lo)*8, lo*8))
		}
		return els
	})
}

// discrsBool reports whether the tag is true exactly for the variants with
// fields: those have discriminant 1, the others 0 (?T: none 0, some 1).
func discrsBool(l *types.Layout) bool {
	for _, v := range l.Variants {
		if want := uint64(min(len(v.Fields), 1)); v.Discr != want {
			return false
		}
	}
	return true
}

// signExtend is the bit pattern d of width bits, sign-extended to 64.
func signExtend(d uint64, bits int) uint64 {
	if bits >= 64 || bits <= 0 {
		return d
	}
	s := uint(64 - bits)
	return uint64(int64(d<<s) >> s)
}
