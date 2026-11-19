// Why: the property tables and their oracle both read the same UCD files, so a
// shared parsing mistake would pass; Go's unicode tables are an independent check.
// What: TestStd_PropsMatchGoUnicode, text.Has against Go's unicode package.
// How: derives each property Go can give (directly or by UAX 44's formulas)
// and compares it with text.Has in release for every scalar Go assigns.

package std

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// std10GoProps gives each checkable property's value for r from Go's
// tables (Unicode 17.0.0); ok false for one Go cannot give.
func std10GoProp(name string, r rune) (bool, bool) {
	is := func(ts ...*unicode.RangeTable) bool { return unicode.In(r, ts...) }
	letter := is(unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl)
	idStart := (letter || is(unicode.Other_ID_Start)) && !is(unicode.Pattern_Syntax, unicode.Pattern_White_Space)
	lower := is(unicode.Ll, unicode.Other_Lowercase)
	upper := is(unicode.Lu, unicode.Other_Uppercase)
	gext := is(unicode.Me, unicode.Mn, unicode.Other_Grapheme_Extend)
	direct := map[string]*unicode.RangeTable{
		"ASCII_Hex_Digit": unicode.ASCII_Hex_Digit, "Bidi_Control": unicode.Bidi_Control, "Dash": unicode.Dash,
		"Deprecated": unicode.Deprecated, "Diacritic": unicode.Diacritic, "Extender": unicode.Extender,
		"Hex_Digit": unicode.Hex_Digit, "IDS_Binary_Operator": unicode.IDS_Binary_Operator,
		"IDS_Trinary_Operator": unicode.IDS_Trinary_Operator, "Ideographic": unicode.Ideographic,
		"Join_Control": unicode.Join_Control, "Logical_Order_Exception": unicode.Logical_Order_Exception,
		"Noncharacter_Code_Point": unicode.Noncharacter_Code_Point, "Pattern_Syntax": unicode.Pattern_Syntax,
		"Pattern_White_Space": unicode.Pattern_White_Space, "Quotation_Mark": unicode.Quotation_Mark,
		"Radical": unicode.Radical, "Regional_Indicator": unicode.Regional_Indicator,
		"Sentence_Terminal": unicode.Sentence_Terminal, "Soft_Dotted": unicode.Soft_Dotted,
		"Terminal_Punctuation": unicode.Terminal_Punctuation, "Unified_Ideograph": unicode.Unified_Ideograph,
		"Variation_Selector": unicode.Variation_Selector, "White_Space": unicode.White_Space,
	}
	if t, ok := direct[name]; ok {
		return is(t), true
	}
	switch name {
	case "ASCII":
		return r < 0x80, true
	case "Any":
		return true, true
	case "Assigned":
		return std10GoAssigned(r), true
	case "Alphabetic":
		return letter || is(unicode.Other_Alphabetic), true
	case "Lowercase":
		return lower, true
	case "Uppercase":
		return upper, true
	case "Cased":
		return lower || upper || is(unicode.Lt), true
	case "Math":
		return is(unicode.Sm, unicode.Other_Math), true
	case "Grapheme_Extend":
		return gext, true
	case "Grapheme_Base":
		return std10GoAssigned(r) && !is(unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp) && !gext, true
	case "ID_Start":
		return idStart, true
	case "ID_Continue":
		return (idStart || is(unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue)) &&
			!is(unicode.Pattern_Syntax, unicode.Pattern_White_Space), true
	case "Default_Ignorable_Code_Point":
		return (is(unicode.Other_Default_Ignorable_Code_Point, unicode.Cf, unicode.Variation_Selector)) &&
			!is(unicode.White_Space, unicode.Prepended_Concatenation_Mark) &&
			!(r >= 0xFFF9 && r <= 0xFFFB) && !(r >= 0x13430 && r <= 0x1343F), true
	}
	return false, false
}

// std10GoChanged are the differences between Go's Unicode 17.0.0 tables and
// UCD 18.0.0 for scalars both assign, as this test found them:
// Unicode 18.0's PropList.txt lists U+0656 (0656..0658), U+06E2
// and U+08D3 (08CA..08D3) as Diacritic, Go's 17.0 tables do not.
var std10GoChanged = map[string]bool{
	"U+0656 Diacritic": true, "U+06E2 Diacritic": true, "U+08D3 Diacritic": true,
}

// std10GoAssigned: r has a General_Category other than Cn in Go's tables
// (the major classes and the four C categories that are not Cn; Go's "C"
// table is not used, since it may hold Cn).
func std10GoAssigned(r rune) bool {
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z,
		unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs)
}

// TestStd_PropsMatchGoUnicode: every scalar that Go's unicode package
// (Unicode 17.0.0) assigns, with each property Has answers that Go's tables
// give, directly or by UAX 44's derivation from them, compared with the bits
// text.Has gives in release. The comparison shares nothing with the
// generator's parse of the UCD files; only the property names and their bit
// order come from std.
//
// Direct from Go's tables: the PropList properties (ASCII_Hex_Digit,
// Bidi_Control, Dash, Deprecated, Diacritic, Extender, Hex_Digit,
// IDS_Binary_Operator, IDS_Trinary_Operator, Ideographic, Join_Control,
// Logical_Order_Exception, Noncharacter_Code_Point, Pattern_Syntax,
// Pattern_White_Space, Quotation_Mark, Radical, Regional_Indicator,
// Sentence_Terminal, Soft_Dotted, Terminal_Punctuation, Unified_Ideograph,
// Variation_Selector, White_Space), and ASCII, Any and Assigned. Derived
// from them by DerivedCoreProperties.txt's stated formulas: Alphabetic,
// Lowercase, Uppercase, Cased, Math, Grapheme_Extend, Grapheme_Base,
// ID_Start, ID_Continue and Default_Ignorable_Code_Point.
//
// Go has no table for the rest, which this test cannot check:
// Bidi_Mirrored, Case_Ignorable, the five Changes_When_* properties and
// Changes_When_NFKC_Casefolded (full case mappings and NFKC), Emoji,
// Emoji_Component, Emoji_Modifier, Emoji_Modifier_Base,
// Emoji_Presentation, Extended_Pictographic, XID_Start, XID_Continue, and
// Script_Extensions (Go's unicode has Scripts only). Those stay checked by
// TestStd_PropsMatchUCD alone.
//
// A scalar whose property changed between Unicode 17.0 and 18.0 differs;
// std10GoChanged lists the changes found, each checked against the 18.0.0
// files, and the test fails on any other difference or on a listed one
// that no longer differs.
func TestStd_PropsMatchGoUnicode(t *testing.T) {
	u := std10NeedUcp(t)
	if unicode.Version != "17.0.0" {
		t.Logf("Go's unicode is %s, not 17.0.0; the check still compares assigned scalars", unicode.Version)
	}
	exe := buildNative(t, nil, std10PropsProgram(u), "--release")
	ctx, cancel := context.WithTimeout(context.Background(), std10RunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")[1:]
	checked := map[string]int{}
	var unchecked []string
	for _, p := range std10Props {
		if _, ok := std10GoProp(p, 'a'); !ok {
			unchecked = append(unchecked, p)
		}
	}
	bad, scalars, k := 0, 0, 0
	seen := map[string]bool{}
	for c := 0; c < std10N; c++ {
		if c >= 0xD800 && c < 0xE000 {
			continue
		}
		line := lines[k]
		k++
		r := rune(c)
		if !std10GoAssigned(r) {
			continue // assigned after Unicode 17.0, or never: Go cannot judge it
		}
		scalars++
		fs := strings.Fields(line)
		bits, err := strconv.ParseUint(fs[2], 16, 64)
		if err != nil {
			t.Fatalf("U+%04X: %q", c, line)
		}
		for i, p := range std10Props {
			want, ok := std10GoProp(p, r)
			if !ok {
				continue
			}
			checked[p]++
			if got := bits>>i&1 == 1; got != want {
				if key := fmt.Sprintf("U+%04X %s", c, p); std10GoChanged[key] {
					seen[key] = true
					continue
				}
				bad++
				if bad <= 30 {
					t.Errorf("U+%04X %s: text.Has %t, Go's unicode %t", c, p, got, want)
				}
			}
		}
	}
	for key := range std10GoChanged {
		if !seen[key] {
			bad++
			t.Errorf("%s: listed as a 17.0-to-18.0 change, but text.Has and Go now agree", key)
		}
	}
	if bad > 0 {
		t.Fatalf("%d differences over %d scalars", bad, scalars)
	}
	t.Logf("%d scalars Go assigns, %d properties checked against Go's tables, equal but for the %d listed 17.0-to-18.0 changes; not checkable from Go: %s, and Script_Extensions",
		scalars, len(checked), len(std10GoChanged), strings.Join(unchecked, ", "))
}
