// Why: goldens and the clang comparison need a lowered signature as text.
// What: printSig and its helpers (paramText, aggregateRet, partTypes): the
// LLVM declaration codegen would write for a lowered signature.
// How: prints the ABI attributes (extension, sret, byval, align, alignstack)
// and the noundef of scalar and indirect parts. The LLVM type of each part
// follows clang's choice for the same C type, so the golden and clang's own
// lowering compare textually.

package abi

import (
	"strconv"
	"strings"

	"mk/internal/types"
)

// printSig renders r lowered as sig for target.
func printSig(w *world, target types.Target, r row, sig Sig) string {
	v := newView(w.tt, w.layout, target)
	var ps []string
	ret := "void"
	switch sig.Ret.Kind {
	case Direct:
		if s, ok := v.scalarOf(r.result); ok {
			ret = "noundef " + extAttr(sig.Ret.Ext) + scalarType(s)
		} else {
			ret = aggregateRet(v, r.result, sig.Ret)
		}
	case Indirect:
		ps = append(ps, "ptr sret("+bytesType(sig.Ret.Size)+") align "+strconv.Itoa(int(sig.Ret.Align)))
	}
	for i, p := range r.params {
		if i == sig.Fixed && sig.Variadic {
			ps = append(ps, "...")
		}
		ps = append(ps, paramText(v, p, sig.Params[i])...)
	}
	if sig.Variadic && sig.Fixed == len(r.params) {
		ps = append(ps, "...")
	}
	return ret + " @" + r.name + "(" + strings.Join(ps, ", ") + ")"
}

func extAttr(e Ext) string {
	switch e {
	case SignExt:
		return "signext "
	case ZeroExt:
		return "zeroext "
	}
	return ""
}

func bytesType(n uint64) string { return "[" + strconv.FormatUint(n, 10) + " x i8]" }

func scalarType(s scalar) string {
	switch {
	case s.isBool:
		return "i1"
	case s.class == leafPtr:
		return "ptr"
	case s.class == leafFloat && s.size == 4:
		return "float"
	case s.class == leafFloat:
		return "double"
	}
	return "i" + strconv.FormatUint(s.size*8, 10)
}

// paramText is the LLVM parameters one Mk parameter becomes.
func paramText(v *view, t types.TypeID, s Slot) []string {
	switch s.Kind {
	case Ignore:
		return nil
	case Indirect:
		if s.Byval {
			return []string{"ptr noundef byval(" + bytesType(s.Size) + ") align " + strconv.Itoa(int(s.Align))}
		}
		return []string{"ptr noundef"}
	}
	if sc, ok := v.scalarOf(t); ok {
		e := strings.TrimSpace(extAttr(s.Ext))
		if e != "" {
			e = " " + e
		}
		return []string{scalarType(sc) + " noundef" + e}
	}
	parts := partTypes(v, t, s, false)
	switch {
	case v.target.Arch == types.ArchX64:
		return parts
	case len(parts) > 1 || s.Parts[0].Class == types.RegFloat:
		// aarch64 passes an array: of pointers when every part is one
		// (clang's choice), else of the part type.
		elem := parts[0]
		for _, p := range parts {
			if p != elem {
				elem = "i64"
			}
		}
		txt := "[" + strconv.Itoa(len(parts)) + " x " + elem + "]"
		if s.AlignStack != 0 {
			txt += " alignstack(" + strconv.Itoa(int(s.AlignStack)) + ")"
		}
		return []string{txt}
	}
	return parts
}

// aggregateRet is the LLVM return type of a Direct aggregate result.
func aggregateRet(v *view, t types.TypeID, s Slot) string {
	parts := partTypes(v, t, s, true)
	switch {
	case len(parts) == 1 && (v.target.Arch == types.ArchX64 || s.Parts[0].Class != types.RegFloat):
		return parts[0]
	case v.target.Arch == types.ArchX64:
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return "[" + strconv.Itoa(len(parts)) + " x " + parts[0] + "]"
}

// partTypes spells each part: float, double, <2 x float>, iN, or ptr where
// clang picks a pointer for the same C type (SysV: an INTEGER eightbyte that
// is exactly one pointer; aarch64: a composite argument of one pointer).
func partTypes(v *view, t types.TypeID, s Slot, result bool) []string {
	fl := v.flatten(t)
	var out []string
	for i, p := range s.Parts {
		switch {
		case p.Class == types.RegVector:
			out = append(out, "<2 x float>")
		case p.Class == types.RegFloat && p.Bytes == 4:
			out = append(out, "float")
		case p.Class == types.RegFloat:
			out = append(out, "double")
		case p.Bytes == 8 && v.pointerPart(fl, uint64(i)*8, result):
			out = append(out, "ptr")
		default:
			out = append(out, "i"+strconv.Itoa(int(p.Bytes)*8))
		}
	}
	return out
}

func (v *view) pointerPart(fl flat, start uint64, result bool) bool {
	switch {
	case v.target.Arch == types.ArchX64 && v.target.OS == types.OSWindows:
		return false
	case v.target.Arch == types.ArchArm64 && result:
		return false
	}
	// The LLVM type at start is clang's: of the leaves beginning there, the
	// first of the largest (a union's storage member); it must be the only
	// data in the eightbyte besides leaves overlapping it.
	var pick *leaf
	for i := range fl.leaves {
		l := &fl.leaves[i]
		if l.off+l.size <= start || l.off >= start+8 {
			continue
		}
		if l.off != start {
			return false
		}
		if pick == nil || l.size > pick.size {
			pick = l
		}
	}
	return pick != nil && pick.class == leafPtr && pick.size == 8
}
