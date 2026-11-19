// Why: a C record's layout is Clang's, taken directly, and a binding may
// declare a Mk struct for it only when Mk lays out the same fields the same
// way; a layout Mk cannot express becomes an opaque type.
//
// What: recLayout and fieldLayout, parseLayouts, which reads Clang's
// `-fdump-record-layouts-complete` text for each record's size, alignment
// and field bit offsets, and mkLayout, the layout Mk gives the mapped
// fields.
//
// How: mkLayout places fields in declaration order with natural alignment,
// capped by a packing value (`packed`, `packed(n)`, which covers
// #pragma pack); records.go compares the two layouts.

package cimport

import (
	"strconv"
	"strings"
)

// recLayout is one record's layout as Clang computed it.
type recLayout struct {
	size, align int64
	fields      []fieldLayout // the record's own members, in order (depth 1)
}

// fieldLayout is one member: its offset in bits and, for a bitfield, its
// width.
type fieldLayout struct {
	bit   int64
	width int // -1 for a plain field
	name  string
}

// parseLayouts reads the dump into a map from dump key ("struct S",
// "union U", "anon:<F:L:C>") to layout.
func parseLayouts(dump string) map[string]*recLayout {
	out := map[string]*recLayout{}
	blocks := strings.Split(dump, "*** Dumping AST Record Layout")
	for _, b := range blocks[1:] {
		lines := strings.Split(b, "\n")
		var key string
		var rl *recLayout
		for _, ln := range lines {
			bar := strings.IndexByte(ln, '|')
			if bar < 0 {
				continue
			}
			left := strings.TrimSpace(ln[:bar])
			right := ln[bar+1:]
			if rl == nil {
				key = dumpKeyOf(strings.TrimSpace(right))
				rl = &recLayout{}
				continue
			}
			if left == "" {
				// "[sizeof=24, align=8]"
				s := strings.TrimSpace(right)
				if strings.HasPrefix(s, "[") {
					for _, part := range strings.Split(strings.Trim(s, "[]"), ",") {
						kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
						if len(kv) != 2 {
							continue
						}
						v, _ := strconv.ParseInt(kv[1], 10, 64)
						switch kv[0] {
						case "sizeof":
							rl.size = v
						case "align":
							rl.align = v
						}
					}
				}
				continue
			}
			// Depth: the indentation after "|" is two spaces per level, one
			// space before the first.
			depth := (len(right) - len(strings.TrimLeft(right, " ")) - 1) / 2
			if depth != 1 {
				continue
			}
			fl := fieldLayout{width: -1}
			if c := strings.IndexByte(left, ':'); c >= 0 {
				byteOff, _ := strconv.ParseInt(left[:c], 10, 64)
				rng := strings.SplitN(left[c+1:], "-", 2)
				lo, _ := strconv.ParseInt(rng[0], 10, 64)
				hi := lo
				if len(rng) == 2 {
					hi, _ = strconv.ParseInt(rng[1], 10, 64)
				}
				fl.bit = byteOff*8 + lo
				fl.width = int(hi - lo + 1)
			} else {
				v, _ := strconv.ParseInt(left, 10, 64)
				fl.bit = v * 8
			}
			text := strings.TrimRight(strings.TrimSpace(right), " ")
			if sp := strings.LastIndexByte(text, ' '); sp >= 0 && !strings.HasSuffix(text, ")") {
				fl.name = text[sp+1:]
			}
			rl.fields = append(rl.fields, fl)
		}
		if rl != nil && key != "" {
			out[key] = rl
		}
	}
	return out
}

// dumpKeyOf is the key of a dump header: "struct S", "union U", or
// "anon:<loc>" for "struct P::(unnamed at F:L:C)".
func dumpKeyOf(h string) string {
	k := dumpKeyRaw(h)
	if strings.HasPrefix(k, "anon:") {
		return "anon:" + normPath(k[len("anon:"):])
	}
	return k
}

func dumpKeyRaw(h string) string {
	if i := strings.Index(h, "(unnamed at "); i >= 0 {
		return "anon:" + strings.TrimSuffix(h[i+len("(unnamed at "):], ")")
	}
	if i := strings.Index(h, "(anonymous at "); i >= 0 {
		return "anon:" + strings.TrimSuffix(h[i+len("(anonymous at "):], ")")
	}
	if i := strings.Index(h, "(unnamed struct at "); i >= 0 {
		return "anon:" + strings.TrimSuffix(h[i+len("(unnamed struct at "):], ")")
	}
	if i := strings.Index(h, "(unnamed union at "); i >= 0 {
		return "anon:" + strings.TrimSuffix(h[i+len("(unnamed union at "):], ")")
	}
	return h
}

// mkLayout is the layout Mk gives a struct of fields with these sizes and
// alignments under packed(pack) (0: natural; 1: packed): each field at
// the next multiple of min(align, pack), the size rounded up to the
// struct's alignment.
func mkLayout(sizes, aligns []int64, pack int64) (offs []int64, size, align int64) {
	align = 1
	off := int64(0)
	for i := range sizes {
		a := aligns[i]
		if pack > 0 && a > pack {
			a = pack
		}
		if a > align {
			align = a
		}
		off = (off + a - 1) / a * a
		offs = append(offs, off)
		off += sizes[i]
	}
	size = (off + align - 1) / align * align
	return offs, size, align
}
