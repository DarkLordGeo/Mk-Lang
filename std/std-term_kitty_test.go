// Why: the kitty keyboard protocol's CSI u reports need an independent model
// for the decoder's oracle and fuzz target to cover them.
// What: termKittyModel, plugged into termModel through termKitty.
// How: written from the kitty keyboard protocol's specification and xterm's
// modifyOtherKeys, not from the Mk code.

// The model of the kitty
// keyboard unit (std/term/input_kitty.mk, an optional unit), plugged
// into termModel (std-term_decode_test.go) through termKitty, so the
// decoder's oracle, TestTerm_DecodeRecorded, TestTerm_DecodeGenerated and
// FuzzTerm_Parse cover CSI u and modifyOtherKeys. Written from the kitty
// keyboard protocol's specification (sw.kovidgoyal.net/kitty/
// keyboard-protocol: the CSI u form, event types, the functional key
// codes) and xterm's modifyOtherKeys. Removing the unit removes this file.

package std

func init() {
	termKitty = termKittyModel
	termRecorded = append(termRecorded,
		"\x1b[27u", "\x1b[27;3u", "\x1b[97;5u", "\x1b[105;5u", "\x1b[13;5u", "\x1b[9;2u", "\x1b[127u", "\x1b[57399u",
		"\x1b[57408;2u", "\x1b[57409u", "\x1b[57414u", "\x1b[57415u", "\x1b[57416u", "\x1b[57417u", "\x1b[57426u",
		"\x1b[57427u", "\x1b[57376u", "\x1b[57383u", "\x1b[57384u", "\x1b[57358u", "\x1b[57441u", "\x1b[57454u",
		"\x1b[57455u", "\x1b[233u", "\x1b[128512u", "\x1b[55296u", "\x1b[57343u", "\x1b[1114111u", "\x1b[9999999u",
		"\x1b[0u", "\x1b[1u", "\x1b[u", "\x1b[97:65u", "\x1b[97:65;6u", "\x1b[97;5:1u", "\x1b[97;5:2u", "\x1b[97;5:3u",
		"\x1b[97;1;97u", "\x1b[1;1:3A", "\x1b[3;5:3~", "\x1b[1;1:3P", "\x1b[1;1:3E", "\x1b[27;5;105~", "\x1b[27;2;13~",
		"\x1b[27;5~", "\x1b[27;1;0~", "\x1b[?1u", "\x1b[>1u", "\x1b\x1b[97;5u", "\x1b\x1b[97;5:3u")
}

// termKittyModel is the oracle of input_kitty.mk's Kitty.
func termKittyModel(n int, v [8]int, count, second int, private, f byte) (termStep, bool) {
	if private != 0 {
		return termStep{}, false
	}
	if second == 3 && count >= 2 && termIsKeyFinal(f) {
		return tDrop(n), true
	}
	mods := func(m int) (bool, bool, bool) {
		if m <= 1 {
			return false, false, false
		}
		m--
		return m&1 != 0, m&2 != 0, m&4 != 0
	}
	var code, m int
	switch {
	case f == 'u' && count >= 1:
		code = v[0]
		if count >= 2 {
			m = v[1]
		}
	case f == '~' && count == 3 && v[0] == 27:
		code, m = v[2], v[1]
	default:
		return termStep{}, false
	}
	key := map[int]int{13: tkEnter, 57414: tkEnter, 9: tkTab, 27: tkEscape, 127: tkBackspace, 57417: tkLeft,
		57418: tkRight, 57419: tkUp, 57420: tkDown, 57421: tkPageUp, 57422: tkPageDown, 57423: tkHome,
		57424: tkEnd, 57425: tkInsert, 57426: tkDelete}
	kp := map[int]rune{57409: '.', 57410: '/', 57411: '*', 57412: '-', 57413: '+', 57415: '=', 57416: ','}
	var s termStep
	if k, ok := key[code]; ok {
		s = tKey(n, k, 0)
	} else if code >= 57376 && code <= 57383 {
		s = tKey(n, tkF, 0)
		s.f = code - 57363
	} else if code >= 57399 && code <= 57408 {
		s = tKey(n, tkChar, rune('0'+code-57399))
	} else if r, ok := kp[code]; ok {
		s = tKey(n, tkChar, r)
	} else if code < 0x20 || code == 0x7f || code >= 0xd800 && code <= 0xdfff || code >= 57344 && code <= 57454 || code > 0x10ffff {
		return tDrop(n), true
	} else {
		s = tKey(n, tkChar, rune(code))
	}
	s.shift, s.alt, s.ctl = mods(m)
	return s, true
}

// termIsKeyFinal is whether f ends a key sequence of xterm's or kitty's.
func termIsKeyFinal(f byte) bool {
	for _, k := range []byte("ABCDEHFPQRS~u") {
		if f == k {
			return true
		}
	}
	return false
}
