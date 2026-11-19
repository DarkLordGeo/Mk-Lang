// Why: the ABI tests need many C-shaped signatures without a checker, so
// they read them from a text suite.
// What: loadSuite and its parser: the rows (row) and declared types (world)
// of testdata/suite.txt.
// How: one declaration or signature per line, in a small type language
// close to Mk's own spelling:
//
//	struct NAME [packed | packed(N)] { a: T, b: T }
//	enum NAME [: TAG] { A, B(T, T), C = 7 }
//	error NAME [{ a: T }]
//	fn NAME(T, T, ..., T, T) R      fixed parameters, "...", variadic arguments
//
// T is a primitive, &T, mut T, *T, []T, mut []T, [N]T, ?T, E!T and E (E an
// error name or names joined by |), (a: T, b: U), fn(T): R, or a declared
// name. Each fn line is one suite row.

package abi

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// row is one suite signature.
type row struct {
	name   string
	line   int
	params []types.TypeID
	fixed  int
	result types.TypeID
	// variadic is set when the row is a variadic call or declaration, even
	// with no variadic argument.
	variadic bool
}

// world is the type universe the suite builds: a table, items with checked
// declarations, and the test's own layout engine (layout_test.go).
type world struct {
	in      *source.Interner
	items   *types.Items
	consts  *types.ConstTable
	tt      *types.Table
	mod     types.ModuleID
	names   map[string]types.ItemID
	arrayN  map[types.TypeID]uint64
	mu      sync.Mutex
	layouts map[types.TypeID]*types.Layout
	rows    []row
}

func newWorld() *world {
	in := source.NewInterner()
	items := types.NewItems()
	consts := types.NewConstTable()
	w := &world{in: in, items: items, consts: consts, tt: types.NewTable(in, items, consts),
		names: map[string]types.ItemID{}, arrayN: map[types.TypeID]uint64{},
		layouts: map[types.TypeID]*types.Layout{}}
	w.mod = items.AddModule(&types.Module{Path: "suite"})
	return w
}

// loadSuite reads testdata/suite.txt.
func loadSuite(t testing.TB) *world {
	t.Helper()
	b, err := os.ReadFile("testdata/suite.txt")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld()
	if err := w.parse(string(b)); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) parse(text string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(parseError); ok {
				err = pe
				return
			}
			panic(r)
		}
	}()
	seen := map[string]bool{}
	for i, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if j := strings.IndexByte(ln, '#'); j >= 0 {
			ln = ln[:j]
		}
		toks := lex(ln)
		if len(toks) == 0 {
			continue
		}
		p := &parser{w: w, toks: toks, line: i + 1}
		switch toks[0] {
		case "struct":
			p.structDecl()
		case "enum":
			p.enumDecl()
		case "error":
			p.errorDecl()
		case "fn":
			r := p.fnRow()
			if seen[r.name] {
				p.fail("duplicate row %s", r.name)
			}
			seen[r.name] = true
			w.rows = append(w.rows, r)
		default:
			p.fail("unknown line")
		}
		if p.pos != len(p.toks) {
			p.fail("trailing %q", p.toks[p.pos])
		}
	}
	return nil
}

type parseError string

func (e parseError) Error() string { return string(e) }

func lex(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case strings.HasPrefix(s[i:], "..."):
			out = append(out, "...")
			i += 3
		case isWord(c):
			j := i
			for j < len(s) && isWord(s[j]) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		default:
			out = append(out, string(c))
			i++
		}
	}
	return out
}

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

type parser struct {
	w    *world
	toks []string
	pos  int
	line int
}

func (p *parser) fail(format string, args ...any) {
	panic(parseError(fmt.Sprintf("suite.txt:%d: ", p.line) + fmt.Sprintf(format, args...)))
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *parser) next() string {
	s := p.peek()
	if s == "" {
		p.fail("unexpected end")
	}
	p.pos++
	return s
}

func (p *parser) expect(s string) {
	if got := p.next(); got != s {
		p.fail("want %q, got %q", s, got)
	}
}

func (p *parser) accept(s string) bool {
	if p.peek() == s {
		p.pos++
		return true
	}
	return false
}

func (p *parser) num() uint64 {
	s := p.next()
	n, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		p.fail("number %q", s)
	}
	return n
}

func (p *parser) addItem(name string, kind types.ItemKind, d types.Decl) types.ItemID {
	if _, dup := p.w.names[name]; dup {
		p.fail("duplicate %s", name)
	}
	id := p.w.items.AddItem(&types.Item{Module: p.w.mod, Name: p.w.in.Intern(name), Kind: kind,
		Key: types.ItemKey("suite", name), Decl: d})
	p.w.names[name] = id
	return id
}

func (p *parser) fields(close string) []types.FieldDecl {
	var fs []types.FieldDecl
	for !p.accept(close) {
		name := p.next()
		p.expect(":")
		fs = append(fs, types.FieldDecl{Name: p.w.in.Intern(name), Type: p.typ()})
		if !p.accept(",") {
			p.expect(close)
			break
		}
	}
	return fs
}

func (p *parser) structDecl() {
	p.expect("struct")
	name := p.next()
	d := &types.StructDecl{}
	if p.accept("packed") {
		d.Packed = 1
		if p.accept("(") {
			d.Packed = uint32(p.num())
			p.expect(")")
		}
	}
	p.expect("{")
	d.Fields = p.fields("}")
	p.addItem(name, types.KindStruct, d)
}

func (p *parser) enumDecl() {
	p.expect("enum")
	name := p.next()
	d := &types.EnumDecl{}
	if p.accept(":") {
		d.Tag = p.typ()
	}
	p.expect("{")
	next := uint64(0)
	for !p.accept("}") {
		v := types.VariantDecl{Name: p.w.in.Intern(p.next())}
		if p.accept("(") {
			for !p.accept(")") {
				v.Fields = append(v.Fields, types.FieldDecl{Type: p.typ()})
				if !p.accept(",") {
					p.expect(")")
					break
				}
			}
		}
		if p.accept("=") {
			next = p.num()
		}
		v.Discr = next
		next++
		d.Variants = append(d.Variants, v)
		if !p.accept(",") {
			p.expect("}")
			break
		}
	}
	p.addItem(name, types.KindEnum, d)
}

func (p *parser) errorDecl() {
	p.expect("error")
	name := p.next()
	d := &types.ErrorDecl{Code: types.ErrorCode(types.ItemKey("suite", name))}
	if p.accept("{") {
		d.Fields = p.fields("}")
	}
	p.addItem(name, types.KindError, d)
}

func (p *parser) fnRow() row {
	p.expect("fn")
	r := row{name: p.next(), line: p.line, fixed: -1}
	p.expect("(")
	for !p.accept(")") {
		if p.accept("...") {
			if r.fixed >= 0 {
				p.fail("two ...")
			}
			r.fixed = len(r.params)
			r.variadic = true
		} else {
			r.params = append(r.params, p.typ())
		}
		if !p.accept(",") {
			p.expect(")")
			break
		}
	}
	if r.fixed < 0 {
		r.fixed = len(r.params)
	}
	r.result = p.typ()
	return r
}

var prims = map[string]types.TypeID{
	"void": types.Void, "never": types.Never, "bool": types.Bool, "char": types.Char,
	"i8": types.I8, "i16": types.I16, "i32": types.I32, "i64": types.I64,
	"u8": types.U8, "u16": types.U16, "u32": types.U32, "u64": types.U64,
	"int": types.Int, "uint": types.Uint, "f32": types.F32, "f64": types.F64, "str": types.Str,
}

func (p *parser) typ() types.TypeID {
	tt := p.w.tt
	s := p.next()
	if id, ok := prims[s]; ok {
		return id
	}
	switch s {
	case "&":
		return tt.Ref(p.typ())
	case "*":
		return tt.RawPtr(p.typ())
	case "?":
		return tt.Optional(p.typ())
	case "mut":
		if p.accept("[") {
			p.expect("]")
			return tt.MutSlice(p.typ())
		}
		return tt.Mut(p.typ())
	case "[":
		if p.accept("]") {
			return tt.Slice(p.typ())
		}
		n := p.num()
		p.expect("]")
		elem := p.typ()
		var b [8]byte
		types.StoreUint(b[:], 8, n)
		c := p.w.consts.Intern(types.ConstValue{Type: types.Int, Align: 8, Bytes: b[:]})
		id := tt.Array(types.ConstArg{Kind: types.CAValue, Value: c, Type: types.Int}, elem)
		p.w.arrayN[id] = n
		return id
	case "(":
		var fs []types.RecordField
		for !p.accept(")") {
			name := p.next()
			p.expect(":")
			fs = append(fs, types.RecordField{Name: p.w.in.Intern(name), Type: p.typ()})
			if !p.accept(",") {
				p.expect(")")
				break
			}
		}
		return tt.Record(fs)
	case "fn":
		p.expect("(")
		var ps []types.FnParam
		for !p.accept(")") {
			ps = append(ps, types.FnParam{Type: p.typ()})
			if !p.accept(",") {
				p.expect(")")
				break
			}
		}
		p.expect(":")
		return tt.Fn(types.FnType{Params: ps, Result: p.typ()})
	}
	id, ok := p.w.names[s]
	if !ok {
		p.fail("unknown type %q", s)
	}
	if p.w.items.Get(id).Kind != types.KindError {
		return tt.Named(id, nil)
	}
	ms := []types.ErrorMember{{Item: id}}
	for p.accept("|") {
		e, ok := p.w.names[p.next()]
		if !ok || p.w.items.Get(e).Kind != types.KindError {
			p.fail("not an error")
		}
		ms = append(ms, types.ErrorMember{Item: e})
	}
	set := tt.ErrorSet(ms)
	if p.accept("!") {
		return tt.ErrorUnion(set, p.typ())
	}
	return tt.ErrorSetType(set)
}
