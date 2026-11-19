// Why: the systems features (a cpu clause, naked and section functions,
// extern vars, align(n) structs, asm statements) are facts a warm cache must
// keep exactly as a cold build has them.
//
// What: writeStrings and readStrings (a cpu clause's feature closure),
// writeAsms and readAsms (a body's asm payloads), funcDeclExtra, and
// InstanceNameCPU, mono's feature-keyed instance name.
//
// How: the fields are written by the hand-written walks they belong to
// (sig.go, decl.go, shape.go, body.go) through these helpers. Where a field
// is absent the encoding is unchanged, so no existing key, symbol or pack
// moves.

package iface

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// writeStrings writes a count, then each string: Signature.CPU, the sorted
// CPUClosure of a cpu clause (nil when absent, which writes as none).
func (w *walker) writeStrings(ss []string) {
	w.e.Uvarint(uint64(len(ss)))
	for _, s := range ss {
		w.e.String(s)
	}
}

// readStrings reads what writeStrings wrote; nil for none.
func readStrings(d *Decoder) []string {
	n := d.Count(1)
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n && d.Err == nil; i++ {
		out = append(out, d.String())
	}
	return out
}

// writeAsms writes a body's asm payloads, in AsmID order: template,
// operands (kind, register, explicit, input operand, output place, const,
// sym item, type), clobber ABI, options and span ID. Span IDs are written
// in both forms, as every SpanID field of a body is.
func (w *walker) writeAsms(as []mir.Asm) {
	e := w.e
	e.Uvarint(uint64(len(as)))
	for i := range as {
		a := &as[i]
		e.String(a.Template)
		e.Uvarint(uint64(len(a.Operands)))
		for j := range a.Operands {
			o := &a.Operands[j]
			e.Uvarint(uint64(o.Kind))
			e.String(o.Reg)
			e.Bool(o.Explicit)
			w.operand(&o.In)
			w.place(o.Out)
			w.cnst(o.Const)
			w.item(o.Sym)
			w.typ(o.Type)
		}
		e.String(a.ClobberABI)
		e.Uvarint(uint64(a.Options))
		w.id32(uint32(a.Span))
	}
}

// readAsms reads what writeAsms wrote; nil for none, and nil when keep is
// false (the dry pass validates without keeping anything).
func (r *reader) readAsms(d *Decoder, keep bool) []mir.Asm {
	n := d.Count(5) // template, operand count, clobber ABI, options, span
	var out []mir.Asm
	if keep && n > 0 {
		out = make([]mir.Asm, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		a := mir.Asm{Template: d.String()}
		m := d.Count(8)
		if keep && m > 0 {
			a.Operands = make([]mir.AsmOperand, 0, m)
		}
		for j := 0; j < m && d.Err == nil; j++ {
			o := mir.AsmOperand{Kind: mir.AsmOperandKind(d.U8()), Reg: d.String(), Explicit: d.Bool()}
			o.In = r.operand(d)
			o.Out = r.place(d)
			o.Const = r.cnst(d, 0)
			o.Sym = r.item(d)
			o.Type = r.typ(d, 0)
			if keep {
				a.Operands = append(a.Operands, o)
			}
		}
		a.ClobberABI = d.String()
		if v := d.Uvarint(); v > 0xffff {
			d.Failf("asm options %#x", v)
		} else {
			a.Options = mir.AsmOptions(v)
		}
		a.Span = mir.SpanID(d.U32())
		if keep {
			out = append(out, a)
		}
	}
	return out
}

// funcDeclExtra mixes a function's naked flag and section into its
// mk.decl hash. A function with neither keeps its mk.sig hash as its
// Decl hash, so no existing key changes.
func (h *Hasher) funcDeclExtra(fd *types.FuncDecl) source.Hash {
	sh := h.Signature(&fd.Sig)
	if !fd.Naked && fd.Section == "" {
		return sh
	}
	var e Encoder
	e.Uvarint(declFunc)
	e.Hash(sh)
	e.Bool(fd.Naked)
	e.String(fd.Section)
	return e.sum(DomDecl)
}

// InstanceNameCPU is the nominal identity of an instance created under the
// feature set cpu: InstanceName itself when cpu is empty, so no existing key,
// symbol or pack changes; else mk.instname.cpu over the generic's key, the
// mk.args hash of its arguments, and the set in the order given (the caller
// passes it sorted, as CPUClosure returns it).
func (h *Hasher) InstanceNameCPU(item types.Key, args []types.GenericArg, cpu []string) source.Hash {
	if len(cpu) == 0 {
		return h.InstanceName(item, args)
	}
	var e Encoder
	e.String(item.String())
	e.Hash(h.GenericArgs(args))
	e.Uvarint(uint64(len(cpu)))
	for _, f := range cpu {
		e.String(f)
	}
	return e.sum(DomInstNameCPU)
}
