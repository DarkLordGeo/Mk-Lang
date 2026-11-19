// Why: mouse and focus reports need an independent model for the decoder's
// oracle and fuzz target to cover them.
// What: termPointerModel, plugged into termModel through termPointer.
// How: written from xterm's ctlseqs, not from the Mk code.

// The model of the mouse
// and focus unit (std/term/input_mouse.mk, an optional unit), plugged
// into termModel (std-term_decode_test.go) through termPointer, so the
// decoder's oracle, TestTerm_DecodeRecorded, TestTerm_DecodeGenerated and
// FuzzTerm_Parse cover the reports. Written from xterm's ctlseqs ("Mouse
// Tracking": the X10 and SGR forms, FocusIn and FocusOut). Removing the
// unit removes this file.

package std

func init() {
	termPointer = termPointerModel
	termRecorded = append(termRecorded,
		"\x1b[<0;1;1M", "\x1b[<0;1;1m", "\x1b[<2;80;24M", "\x1b[<32;5;5M", "\x1b[<35;5;5M", "\x1b[<64;1;1M",
		"\x1b[<65;1;1M", "\x1b[<66;1;1M", "\x1b[<128;1;1M", "\x1b[<28;3;4M", "\x1b[<0;0;0M", "\x1b[<0;9999999;1M",
		"\x1b[<0;1M", "\x1b[<0;1;2;3M", "\x1b[<0:1;2;3M", "\x1b[<M", "\x1b[<0;1;1", "\x1b[M#*+", "\x1b[M\x01!!",
		"\x1b[M \xff\xff", "\x1b[M !", "\x1b[M", "\x1b[1I", "\x1b[?I", "\x1b[>O", "\x1b\x1b[<0;1;1M", "\x1b\x1b[I",
		"\x1b\x1b[200~", "\x1b\x1b[?1;2c")
}

// termPointerModel is the oracle of input_mouse.mk's Pointer.
func termPointerModel(b []byte, n int, v [8]int, count int, private, f byte, final bool) (termStep, bool) {
	mouse := func(n, button, cb, row, col int) termStep {
		return termStep{n: n, kind: 3, shift: cb&4 != 0, alt: cb&8 != 0, ctl: cb&16 != 0,
			a: button, b: max(row, 0), c: max(col, 0)}
	}
	if private == '<' && (f == 'M' || f == 'm') {
		if count != 3 {
			return tDrop(n), true
		}
		button := v[0] & 0xe3
		if f == 'm' {
			button = 3
		}
		return mouse(n, button, v[0], v[2]-1, v[1]-1), true
	}
	if private != 0 || count != 0 {
		return termStep{}, false
	}
	switch {
	case f == 'I':
		return termStep{n: n, kind: 4, a: 1}, true
	case f == 'O':
		return termStep{n: n, kind: 4}, true
	case f != 'M' || n != 3:
		return termStep{}, false
	case len(b) < 6:
		if final {
			return tDrop(len(b)), true
		}
		return tDrop(0), true
	case b[3] < 0x20 || b[4] < 0x20 || b[5] < 0x20:
		return tDrop(3), true
	}
	cb := int(b[3]) - 32
	return mouse(6, cb&0xe3, cb, int(b[5])-33, int(b[4])-33), true
}
