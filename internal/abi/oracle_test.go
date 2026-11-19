// Why: the only trustworthy reference for a C ABI is a C compiler, so the
// lowering is compared with clang's own on every suite row.
// What: TestABIAgreesWithClang, the record reader and the normalizers
// (norm, normElem, canonType, llvmSize).
// How: testdata/clang/<target>.txt is clang's lowering of the C declarations
// equivalent to every suite row, produced by a probe kept outside this
// repository (no C is in the repository and no test compiles C). The test
// compares it with Lower after removing what is not ABI: value names,
// noundef and clang's optimization facts on sret pointers, and the spelling
// of struct types (an sret or byval type compares by size; a struct of n
// equal members compares equal to an array of n of them, which LLVM passes
// alike).

package abi

import (
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

// oraclePath is the clang record of one target.
func oraclePath(target types.Target) string { return "testdata/clang/" + target.Name + ".txt" }

// TestABIAgreesWithClang checks, for every target with a clang record (the
// two x86_64 targets are required), that the record names its clang version
// and command, that the golden header names the same, that every suite row
// is present, that each lowered signature equals clang's, and that the C
// types the probe wrote have the layouts this package's tests assume.
func TestABIAgreesWithClang(t *testing.T) {
	for _, target := range abiTargets() {
		t.Run(target.Name, func(t *testing.T) {
			o, err := readOracle(oraclePath(target))
			if err != nil {
				if target.Arch == types.ArchX64 {
					t.Fatalf("required clang record: %v", err)
				}
				t.Skipf("no clang record: %v", err)
			}
			agreeWithClang(t, target, o)
		})
	}
}

type oracle struct {
	*golden
	layouts [][3]string // size, align, spelling
	offsets [][2]string // comma-separated field offsets, spelling
}

func readOracle(path string) (*oracle, error) {
	g, err := readGolden(path)
	if err != nil {
		return nil, err
	}
	o := &oracle{golden: g}
	b, _ := readFileLines(path)
	for _, ln := range b {
		if rest, ok := strings.CutPrefix(ln, "=layout "); ok {
			f := strings.SplitN(rest, " ", 3)
			if len(f) == 3 {
				o.layouts = append(o.layouts, [3]string{f[0], f[1], f[2]})
			}
		}
		if rest, ok := strings.CutPrefix(ln, "=offsets "); ok {
			if offs, spell, ok := strings.Cut(rest, " "); ok {
				o.offsets = append(o.offsets, [2]string{offs, spell})
			}
		}
	}
	return o, nil
}

func agreeWithClang(t *testing.T, target types.Target, o *oracle) {
	var clang, command string
	for _, h := range o.header {
		if v, ok := strings.CutPrefix(h, "# clang: "); ok {
			clang = v
		}
		if v, ok := strings.CutPrefix(h, "# command: "); ok {
			command = v
		}
	}
	if !strings.HasPrefix(clang, "clang version ") || !strings.Contains(command, "--target="+target.LLVMTriple+" ") {
		t.Fatalf("record header names clang %q, command %q", clang, command)
	}
	g, err := readGolden("testdata/" + target.Name + ".golden")
	if err != nil {
		t.Fatal(err)
	}
	hdr := strings.Join(g.header, "\n")
	if !strings.Contains(hdr, "cross-checked against clang: "+clang) || !strings.Contains(hdr, "cross-checked against command: "+command) {
		t.Errorf("golden header does not record the cross-check: %s", hdr)
	}
	w := loadSuite(t)
	agreed, skipped := 0, 0
	for _, r := range w.rows {
		want, ok := o.lines[r.name]
		if !ok {
			t.Errorf("row %s missing from the clang record", r.name)
			continue
		}
		sig := lowerRow(w, target, r)
		if why, ok := strings.CutPrefix(want, "SKIP "); ok {
			if !hasZeroSized(sig, r) && !w.rowHoldsZeroSized(r) {
				t.Errorf("row %s skipped (%s) but has no zero-sized value", r.name, why)
			}
			skipped++
			continue
		}
		got := printSig(w, target, r, sig)
		if norm(got) != norm(want) {
			t.Errorf("%s (suite.txt:%d) disagrees with clang\n  abi   %s\n  clang %s\n  (normalized %s vs %s)",
				r.name, r.line, got, want, norm(got), norm(want))
			continue
		}
		agreed++
	}
	for _, l := range o.layouts {
		p := &parser{w: w, toks: lex(l[2]), line: 0}
		ty := p.typ()
		size, align := w.sa(ty)
		if strconv.FormatUint(size, 10) != l[0] || strconv.FormatUint(uint64(align), 10) != l[1] {
			t.Errorf("layout of %s: tests assume size %d align %d, the probe's C type has %s %s", l[2], size, align, l[0], l[1])
		}
	}
	for _, l := range o.offsets {
		p := &parser{w: w, toks: lex(l[1]), line: 0}
		var offs []string
		for _, f := range w.layout(p.typ()).Fields {
			offs = append(offs, strconv.FormatUint(f.Offset, 10))
		}
		if got := strings.Join(offs, ","); got != l[0] {
			t.Errorf("field offsets of %s: tests assume %s, the probe's C type has %s", l[1], got, l[0])
		}
	}
	t.Logf("%s: %d rows agree with %s, %d zero-sized rows skipped, %d C layouts and %d field-offset lists checked",
		target.Name, agreed, clang, skipped, len(o.layouts), len(o.offsets))
}

func hasZeroSized(s Sig, r row) bool {
	for _, p := range s.Params {
		if p.Kind == Ignore {
			return true
		}
	}
	return s.Ret.Kind == Ignore && r.result != types.Void && r.result != types.Never
}

// rowHoldsZeroSized reports whether some parameter or the result holds a
// zero-sized field (the probe cannot write such a type in C for MSVC, where an
// empty C struct takes 4 bytes).
func (w *world) rowHoldsZeroSized(r row) bool {
	var holds func(t types.TypeID) bool
	holds = func(t types.TypeID) bool {
		if s, _ := w.sa(t); s == 0 {
			return t != types.Void && t != types.Never
		}
		switch ty := w.tt.Get(t); ty.Kind {
		case types.KArray, types.KOptional:
			return holds(ty.Elem)
		case types.KRecord, types.KNamed:
			for _, f := range w.layout(t).Fields {
				if holds(f.Type) {
					return true
				}
			}
		}
		return false
	}
	for _, p := range append([]types.TypeID{r.result}, r.params...) {
		if holds(p) {
			return true
		}
	}
	return false
}

// norm reduces a declaration "<ret> @name(<params>)" to its ABI content.
func norm(decl string) string {
	at := strings.Index(decl, " @")
	open := strings.IndexByte(decl[at:], '(') + at
	ret := normElem(decl[:at], true)
	var ps []string
	for _, p := range splitTop(decl[open+1 : len(decl)-1]) {
		ps = append(ps, normElem(p, false))
	}
	return ret + " (" + strings.Join(ps, ", ") + ")"
}

var dropAttr = map[string]bool{"noundef": true, "dead_on_unwind": true, "noalias": true, "writable": true,
	"dead_on_return": true, "nonnull": true}

// normElem normalizes one return ("attrs type") or parameter ("type attrs").
func normElem(s string, ret bool) string {
	s = strings.TrimSpace(s)
	var attrs []string
	if ret {
		for {
			w, rest, _ := strings.Cut(s, " ")
			if w != "noundef" && w != "zeroext" && w != "signext" {
				break
			}
			if !dropAttr[w] {
				attrs = append(attrs, w)
			}
			s = rest
		}
		return strings.Join(append(attrs, canonType(s)), " ")
	}
	ty, rest := cutType(s)
	out := []string{canonType(ty)}
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		var a string
		if i := strings.IndexByte(rest, '('); i >= 0 && (strings.IndexByte(rest, ' ') < 0 || i < strings.IndexByte(rest, ' ')) {
			c := closing(rest, i)
			a, rest = rest[:c+1], rest[c+1:]
			name, inner := a[:i], a[i+1:len(a)-1]
			if name == "sret" || name == "byval" {
				a = name + "(" + strconv.FormatUint(llvmSize(inner), 10) + ")"
			}
		} else {
			a, rest, _ = strings.Cut(rest, " ")
		}
		if !dropAttr[a] {
			out = append(out, a)
		}
	}
	return strings.Join(out, " ")
}

// cutType splits a leading LLVM type from the rest.
func cutType(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '{', '[', '<':
		c := closing(s, 0)
		return s[:c+1], s[c+1:]
	}
	w, rest, _ := strings.Cut(s, " ")
	return w, rest
}

// closing is the index of the bracket closing the one at i.
func closing(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '(', '{', '[', '<':
			depth++
		case ')', '}', ']', '>':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(s) - 1
}

// splitTop splits at depth-0 commas.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{', '[', '<':
			depth++
		case ')', '}', ']', '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if r := strings.TrimSpace(s[start:]); r != "" {
		out = append(out, r)
	}
	return out
}

// canonType writes a struct of n equal members as [n x T], and an aggregate
// whose leaves are all one float type as [n x float] or [n x double]: an
// aarch64 HFA result that clang spells as its C struct, and LLVM returns in
// the same v registers.
func canonType(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return s
	}
	if ls := leavesOf(s); len(ls) > 0 && (ls[0] == "float" || ls[0] == "double") {
		same := true
		for _, l := range ls {
			same = same && l == ls[0]
		}
		if same {
			return "[" + strconv.Itoa(len(ls)) + " x " + ls[0] + "]"
		}
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return s
	}
	es := splitTop(s[1 : len(s)-1])
	for _, e := range es {
		if canonType(e) != canonType(es[0]) {
			return s
		}
	}
	return "[" + strconv.Itoa(len(es)) + " x " + canonType(es[0]) + "]"
}

// leavesOf flattens an LLVM struct or array type to its scalar types.
func leavesOf(s string) []string {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"):
		var out []string
		for _, e := range splitTop(s[1 : len(s)-1]) {
			out = append(out, leavesOf(e)...)
		}
		return out
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
		n, elem, _ := strings.Cut(s[1:len(s)-1], " x ")
		k, _ := strconv.Atoi(n)
		var out []string
		for range k {
			out = append(out, leavesOf(elem)...)
		}
		return out
	}
	return []string{s}
}

// llvmSize is the allocation size of an LLVM type under the x86_64 and
// aarch64 data layouts (natural alignment; <{ }> packed).
func llvmSize(s string) uint64 {
	size, _ := llvmSizeAlign(strings.TrimSpace(s))
	return size
}

func llvmSizeAlign(s string) (uint64, uint64) {
	// clang spells an array of structs inside a packed struct "[1 x { i32 } ]".
	s = strings.TrimSpace(s)
	switch {
	case s == "ptr" || s == "double" || s == "i64":
		return 8, 8
	case s == "float" || s == "i32":
		return 4, 4
	case s == "i16":
		return 2, 2
	case s == "i8" || s == "i1":
		return 1, 1
	case s == "<2 x float>":
		return 8, 8
	case strings.HasPrefix(s, "["):
		n, elem, _ := strings.Cut(s[1:len(s)-1], " x ")
		k, _ := strconv.ParseUint(n, 10, 64)
		es, ea := llvmSizeAlign(elem)
		return k * es, ea
	case strings.HasPrefix(s, "<{"):
		var size uint64
		for _, e := range splitTop(s[2 : len(s)-2]) {
			es, _ := llvmSizeAlign(e)
			size += es
		}
		return size, 1
	case strings.HasPrefix(s, "{"):
		var size, align uint64 = 0, 1
		for _, e := range splitTop(s[1 : len(s)-1]) {
			es, ea := llvmSizeAlign(e)
			size = (size + ea - 1) / ea * ea
			size += es
			align = max(align, ea)
		}
		return (size + align - 1) / align * align, align
	}
	return 0, 1
}
