// Why: output must never depend on an ID or on intern order, so array
// lengths, const generic arguments and MIR const operands must print by
// value, not by ConstID.
//
// What: Table.ConstString and GenericArgString, with the writers they share
// (writeConst, writeScalarConst, writeFnRef, writeGenericArg).
//
// How: a scalar or str value is spelled as a literal; any other value as
// its type, alignment and bytes in hex, then each relocation with its
// target spelled recursively (a const by value, a function by its name).

package types

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"mk/internal/source"
)

// ConstString spells an interned const value by its value, never by its
// ConstID: integers in decimal, bool as true or false, char and str quoted,
// floats in shortest form; any other value as its type and its canonical
// bytes, {align A 0x.. @off &pointee, @off fn key[args]}, with pointees
// spelled recursively. Two tables that interned equal values in different
// orders spell them the same. The table must have been made with a
// ConstTable; otherwise it is an ICE.
func (t *Table) ConstString(id ConstID) string {
	var b strings.Builder
	t.writeConst(&b, id)
	return b.String()
}

func (t *Table) writeConst(b *strings.Builder, id ConstID) {
	if t.consts == nil {
		source.ICEf(source.Span{}, "types.Table: const %d spelled without a ConstTable", id)
	}
	v := t.consts.Get(id)
	if t.writeScalarConst(b, v) {
		return
	}
	t.writeType(b, v.Type, nil)
	b.WriteString("{align ")
	b.WriteString(strconv.Itoa(int(v.Align)))
	b.WriteString(" 0x")
	b.WriteString(hex.EncodeToString(v.Bytes))
	for _, r := range v.Relocs {
		b.WriteString(", @")
		b.WriteString(strconv.Itoa(int(r.Offset)))
		b.WriteByte(' ')
		switch r.Kind {
		case RelocData:
			b.WriteByte('&')
			t.writeConst(b, r.Data)
		case RelocFn:
			b.WriteString("fn ")
			t.writeFnRef(b, r.Fn)
		default:
			b.WriteString("?")
		}
	}
	b.WriteByte('}')
}

// writeScalarConst spells a scalar or str value directly and reports whether
// it did.
func (t *Table) writeScalarConst(b *strings.Builder, v *ConstValue) bool {
	ty := t.Get(v.Type)
	n := len(v.Bytes)
	plain := len(v.Relocs) == 0
	switch {
	case ty.IsInteger() && plain && (n == 1 || n == 2 || n == 4 || n == 8):
		x := LoadUint(v.Bytes, n)
		if ty.Signed {
			shift := uint(64 - 8*n)
			b.WriteString(strconv.FormatInt(int64(x<<shift)>>shift, 10))
		} else {
			b.WriteString(strconv.FormatUint(x, 10))
		}
		return true
	case ty.Kind == KBool && plain && n == 1:
		b.WriteString(strconv.FormatBool(v.Bytes[0] != 0))
		return true
	case ty.Kind == KChar && plain && n == 4:
		b.WriteString(strconv.QuoteRune(rune(LoadUint(v.Bytes, 4))))
		return true
	case ty.Kind == KFloat && plain && n == 4:
		b.WriteString(strconv.FormatFloat(float64(math.Float32frombits(uint32(LoadUint(v.Bytes, 4)))), 'g', -1, 32))
		return true
	case ty.Kind == KFloat && plain && n == 8:
		b.WriteString(strconv.FormatFloat(math.Float64frombits(LoadUint(v.Bytes, 8)), 'g', -1, 64))
		return true
	case ty.Kind == KStr && n == 16 && len(v.Relocs) == 1 && v.Relocs[0].Offset == 0 && v.Relocs[0].Kind == RelocData:
		data := t.consts.Get(v.Relocs[0].Data).Bytes
		if l := LoadUint(v.Bytes[8:], 8); l <= uint64(len(data)) {
			b.WriteString(strconv.Quote(string(data[:l])))
			return true
		}
	}
	return false
}

// writeFnRef spells a function by its item's key and its generic arguments.
func (t *Table) writeFnRef(b *strings.Builder, f FnRef) {
	b.WriteString(t.items.Get(f.Item).Key.String())
	if len(f.Args) == 0 {
		return
	}
	b.WriteByte('[')
	for i, a := range f.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		t.writeGenericArg(b, a, nil)
	}
	b.WriteByte(']')
}

// GenericArgString spells one generic argument: a type, or a const argument
// by ConstArgString.
func (t *Table) GenericArgString(a GenericArg, generics []GenericParam) string {
	var b strings.Builder
	t.writeGenericArg(&b, a, generics)
	return b.String()
}

func (t *Table) writeGenericArg(b *strings.Builder, a GenericArg, gs []GenericParam) {
	if a.IsConst {
		t.writeConstArg(b, a.Const, gs)
	} else {
		t.writeType(b, a.Type, gs)
	}
}
