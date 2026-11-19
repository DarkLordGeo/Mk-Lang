// Why: each language construct has one MIR shape that borrow, mono and
// codegen expect; these tests name and pin them.
// What: TestR13_* (evaluation order), TestR14_* (temporaries), TestR21_* and
// the else-exit tests (defer and onerr exits), TestTry to TestScopeJoin,
// the index, Must, cast, contract, fields-loop and match shapes, and
// cleanupCopies.
// How: each builds source through the front end and checks the printed MIR
// (Validate runs on every body).

package lower

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
)

// wantAll fails unless every fragment occurs in text, in any order.
func wantAll(t *testing.T, text string, frags ...string) {
	t.Helper()
	for _, f := range frags {
		if !strings.Contains(text, f) {
			t.Fatalf("missing %q in:\n%s", f, text)
		}
	}
}

// count is how many times frag occurs in text.
func count(text, frag string) int { return strings.Count(text, frag) }

func TestR13_BorrowsFormedLast(t *testing.T) {
	l := lowerSrc(t, `I(): int {
	return 0
}
G(): int {
	return 1
}
F(x: mut int, y: int) {
}
H(xs: mut [4]int) {
	F(mut xs[I()], G())
}
`)
	wantIn(t, l.print("H"), "call m.G()", "call m.I()", "Lt(", "panic bounds", "= mut ", "call m.F(")
}

func TestR13_NestedBorrowFormedWithOperand(t *testing.T) {
	l := lowerSrc(t, `G(p: &int): int {
	return *p
}
F(a: int, b: mut int) {
}
H() {
	var x := 1
	F(G(&x), mut x)
}
`)
	wantIn(t, l.print("H"), "= &_1", "call m.G(", "= mut _1", "call m.F(")
}

func TestR13_SameExchange(t *testing.T) {
	l := lowerSrc(t, `Exchange(a: mut int, b: mut int) {
}
H() {
	var n := 1
	Exchange(mut n, mut n)
}
`)
	h := l.print("H")
	if count(h, "= mut _1") != 2 {
		t.Fatalf("want two exclusive borrows of n:\n%s", h)
	}
}

func TestR13_AssignRightFirst(t *testing.T) {
	l := lowerSrc(t, `I(): int {
	return 0
}
F(): int {
	return 1
}
H(xs: mut [4]int) {
	xs[I()] = F()
}
`)
	wantIn(t, l.print("H"), "call m.F()", "call m.I()", "panic bounds")
}

func TestR13_CompoundPlaceOnce(t *testing.T) {
	l := lowerSrc(t, `I(): int {
	return 0
}
F(): int {
	return 1
}
H(xs: mut [4]int) {
	xs[I()] += F()
}
`)
	h := l.print("H")
	wantIn(t, h, "call m.F()", "call m.I()", "Add(")
	if count(h, "call m.I()") != 1 {
		t.Fatalf("I called more than once:\n%s", h)
	}
}

func TestR13_ByValueLocalReadInOrder(t *testing.T) {
	l := lowerSrc(t, `Next(n: mut int): int {
	*n += 1
	return *n
}
Pair(a: int, b: int): int {
	return a
}
H(): int {
	var n := 1
	return Pair(n, Next(mut n))
}
D(a: int, b: int): int {
	return Pair(a, b)
}
`)
	h := l.print("H")
	// The slot: n is read into a temporary before Next runs.
	wantIn(t, h, "= copy _1", "call m.Next(", "call m.Pair(copy _")
	if strings.Contains(h, "call m.Pair(copy _1,") {
		t.Fatalf("n passed directly after a non-inert operand:\n%s", h)
	}
	wantIn(t, l.print("D"), "call m.Pair(copy _1, copy _2)")
}

func TestR13_ConstructorWrittenOrder(t *testing.T) {
	l := lowerSrc(t, `struct Point {
	x: int
	y: int
}
A(): int {
	return 1
}
B(): int {
	return 2
}
H(): Point {
	return Point(y: A(), x: B())
}
`)
	wantIn(t, l.print("H"), "call m.A()", "call m.B()", "m.Point(")
}

func TestR14_TempEndsAtStatement(t *testing.T) {
	l := lowerSrc(t, `struct Point {
	x: int
}
MakePoint(): Point {
	return Point(x: 1)
}
Use(p: &Point) {
}
H() {
	Use(&MakePoint())
	Use(&MakePoint())
}
`)
	h := l.print("H")
	wantIn(t, h, "call m.MakePoint()", "call m.Use(", "scope_end", "call m.MakePoint()")
}

func TestR21_DeferOnEveryExit(t *testing.T) {
	l := lowerSrc(t, `error E
Log() {
}
F(n: int): E!int {
	defer Log()
	for i in 0..n {
		if i == 1 {
			continue
		}
		if i == 2 {
			break
		}
	}
	if n == 3 {
		fail E
	}
	if n == 4 {
		return 4
	}
	return 0
}
`)
	f := l.print("F")
	// continue, break, fail, return 4, return 0: each leaves the defer's
	// block (the function scope only for the last three).
	if c := count(f, "(defer 0):"); c < 3 {
		t.Fatalf("want a defer copy per function exit, got %d blocks:\n%s", c, f)
	}
}

func TestR21_OnerrOnErrorExitsOnly(t *testing.T) {
	l := lowerSrc(t, `error E
Log() {
}
Load(): E!int {
	return 1
}
F(n: int): E!int {
	onerr Log()
	v := try Load()
	if n == 3 {
		fail E
	}
	return v
}
`)
	f := l.print("F")
	b := l.body("F")
	onerrs := 0
	for _, blk := range b.Blocks {
		if blk.Origin == mir.OriginOnerr && blk.Term.Kind == mir.TermCall {
			onerrs++
		}
	}
	if onerrs != 2 {
		t.Fatalf("want onerr copies on the try and fail edges only, got %d:\n%s", onerrs, f)
	}
}

func TestReturnInElseRunsNoOnerr(t *testing.T) {
	l := lowerSrc(t, `error E
Log() {
}
Load(): E!int {
	return 1
}
F(): E!int {
	onerr Log()
	v := Load() else {
		return 0
	}
	return v
}
`)
	f := l.print("F")
	wantIn(t, f, "return else")
	if strings.Contains(f, "onerr 0") {
		t.Fatalf("a return from an else handler ran onerr:\n%s", f)
	}
}

func TestBreakLeavingElseRunsOnerr(t *testing.T) {
	l := lowerSrc(t, `error E
Log() {
}
Load(): E!int {
	return 1
}
F() {
	for {
		onerr Log()
		v := Load() else {
			break
		}
		Use(v)
	}
}
Use(v: int) {
}
`)
	wantAll(t, l.print("F"), "(onerr 0):", "else-exit")
}

func TestBreakFromOptionalElse(t *testing.T) {
	l := lowerSrc(t, `Log() {
}
Next(): ?int {
	return none
}
F() {
	for {
		onerr Log()
		v := Next() else {
			break
		}
		Use(v)
	}
}
Use(v: int) {
}
`)
	f := l.print("F")
	if strings.Contains(f, "onerr 0") || strings.Contains(f, "else-exit") {
		t.Fatalf("a break from a ?T else handler is an error exit:\n%s", f)
	}
}

func TestBreakInsideHandlerLoop(t *testing.T) {
	l := lowerSrc(t, `error E
Log() {
}
Load(): E!int {
	return 1
}
F(): int {
	v := Load() else {
		for {
			onerr Log()
			break
		}
		return 0
	}
	return v
}
`)
	f := l.print("F")
	if strings.Contains(f, "onerr 0") || strings.Contains(f, "else-exit") {
		t.Fatalf("a break of a loop inside a handler is an error exit:\n%s", f)
	}
}

func TestCleanupEntrySinglePred(t *testing.T) {
	l, validate := lowerNoValidate(t, `error E
Log(n: int) {
}
F(n: int): E!int {
	defer {
		if n > 1 {
			Log(n)
		}
	}
	if n == 3 {
		fail E
	}
	return 0
}
`)
	b := l.body("F")
	entries, joins, err := cleanupCopies(b)
	if err != "" {
		t.Fatalf("%s:\n%s", err, l.print("F"))
	}
	// Two exits (the fail and the return), so two copies of defer 0, each
	// with the if's join inside it.
	if len(entries) != 2 || joins != 2 {
		t.Fatalf("%d copies with %d inner joins, want 2 and 2:\n%s", len(entries), joins, l.print("F"))
	}
	// The check is not vacuous: send the second exit edge into the first
	// copy, and both this predicate and Validate's M7 refuse it.
	if err := validate("F"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for i := range b.Blocks {
		tm := &b.Blocks[i].Term
		if tm.Kind == mir.TermGoto && tm.Target == entries[1] {
			tm.Target = entries[0]
		}
	}
	if _, _, err := cleanupCopies(b); err == "" {
		t.Fatalf("a shared copy passed the predicate:\n%s", l.print("F"))
	}
	if err := validate("F"); err == nil || !strings.Contains(err.Error(), "M7") {
		t.Fatalf("Validate on a shared copy: %v", err)
	}
}

// cleanupCopies is M7 restated independently of mir's validator: a copy's
// entry is a cleanup block whose predecessors are all outside its Origin
// and Defer, and the copy is what it reaches inside them. Each entry has
// exactly one predecessor (one exit edge), copies are disjoint, and every
// other cleanup block is entered only from its own copy. It returns the
// entries, the cleanup blocks with several predecessors (joins inside a
// copy), and the first violation.
func cleanupCopies(b *mir.Body) (entries []mir.BlockID, joins int, err string) {
	preds := make([][]mir.BlockID, len(b.Blocks))
	for i := range b.Blocks {
		for _, s := range b.Successors(mir.BlockID(i), nil) {
			preds[s] = append(preds[s], mir.BlockID(i))
		}
	}
	same := func(x, y mir.BlockID) bool {
		return b.Blocks[x].Origin == b.Blocks[y].Origin && b.Blocks[x].Defer == b.Blocks[y].Defer
	}
	owner := make([]int, len(b.Blocks))
	for i, blk := range b.Blocks {
		x := mir.BlockID(i)
		if blk.Origin == mir.OriginBody {
			continue
		}
		outside := 0
		for _, p := range preds[x] {
			if !same(p, x) {
				outside++
			}
		}
		if outside == 0 {
			continue
		}
		if len(preds[x]) != 1 {
			return nil, 0, fmt.Sprintf("copy entry bb%d has predecessors %v, want one", x, preds[x])
		}
		entries = append(entries, x)
		work := []mir.BlockID{x}
		for len(work) > 0 {
			y := work[len(work)-1]
			work = work[:len(work)-1]
			if owner[y] == len(entries) {
				continue
			}
			if owner[y] != 0 {
				return nil, 0, fmt.Sprintf("bb%d is in two copies", y)
			}
			owner[y] = len(entries)
			for _, s := range b.Successors(y, nil) {
				if same(y, s) {
					work = append(work, s)
				}
			}
		}
	}
	for i, blk := range b.Blocks {
		if blk.Origin == mir.OriginBody || owner[i] == 0 {
			continue
		}
		for _, p := range preds[i] {
			if owner[p] != owner[i] && mir.BlockID(i) != entries[owner[i]-1] {
				return nil, 0, fmt.Sprintf("bb%d enters copy %d at bb%d, not its entry", p, owner[i], i)
			}
		}
		if len(preds[i]) > 1 {
			joins++
		}
	}
	return entries, joins, ""
}

func TestTry(t *testing.T) {
	l := lowerSrc(t, `error NotFound
error Denied
Load(id: int): NotFound!int {
	return id
}
Show(id: int): NotFound|Denied!int {
	v := try Load(id)
	return v
}
`)
	wantIn(t, l.print("Show"), "err try _", "WidenErrors[", "= err(move ", "return try")
}

func TestElseAndMatch(t *testing.T) {
	l := lowerSrc(t, `error E
F(x: int): E!int {
	return x
}
Drop(e: E) {
}
Quiet(x: int): int {
	v := F(x) else e {
		Drop(e)
		return 0
	}
	return v
}
M(x: int): int {
	match F(x) {
		ok(v) {
			return v
		}
		err(e) {
			Drop(e)
			return 0
		}
	}
}
`)
	wantIn(t, l.print("Quiet"), "err else _", "return else")
	wantIn(t, l.print("M"), "err match _", "return normal", "return normal")
}

func TestFailAndReturn(t *testing.T) {
	l := lowerSrc(t, `error NotFound(id: int)
error Denied
Load(id: int): NotFound|Denied!str {
	if id < 0 {
		fail Denied
	}
	if id > 9 {
		fail NotFound(id: id)
	}
	return "data"
}
`)
	f := l.print("Load")
	wantIn(t, f, "WidenErrors[", "return fail new-error", "return fail new-error", "= ok(", "return normal")
}

func TestIfBind(t *testing.T) {
	l := lowerSrc(t, `Next(): ?int {
	return none
}
F(o: ?int): int {
	if x := Next() {
		return x
	}
	var p: ?int = o
	if y := mut p {
		*y = 2
	}
	if z := &p {
		return *z
	}
	return 0
}
`)
	wantIn(t, l.print("F"), "Discriminant(", "as some", "Discriminant(", "= mut (", "Discriminant(", "= &(")
}

func TestForForms(t *testing.T) {
	l := lowerSrc(t, `F(xs: []int, ys: mut []int, var a: [3]int): int {
	var s := 0
	for x in xs {
		s += x
	}
	for x in &xs {
		s += *x
	}
	for x in mut ys {
		*x += 1
	}
	for i, x in a {
		s += x + i
	}
	for x in mut a {
		*x = 0
	}
	return s
}
`)
	f := l.print("F")
	wantIn(t, f, "Len(", "AddNoWrap", "holder(loop2)", "elem(loop2)", "holder(loop4)", "elem(loop4)")
	if strings.Contains(f, "panic bounds") {
		t.Fatalf("a for form carries a bounds check:\n%s", f)
	}
}

func TestR14_ForHeaderPerIteration(t *testing.T) {
	l := lowerSrc(t, `Next(n: int): ?int {
	return none
}
F() {
	var x2 := 1
	for x := Next(x2) {
		x2 = x
	}
}
`)
	f := l.print("F")
	if count(f, "scope_end") < 2 {
		t.Fatalf("the header temporary must end on both edges:\n%s", f)
	}
}

// TestArchCounterIncrementWraps: the range counter's increment has no
// overflow check; it is AddNoWrap, which never traps and lets codegen mark
// it nsw or nuw.
func TestArchCounterIncrementWraps(t *testing.T) {
	l := lowerSrc(t, `F(n: int): int {
	var s := 0
	for i in 0..n {
		s += i
	}
	return s
}
`)
	wantIn(t, l.print("F"), "Lt(", "AddNoWrap(")
	if strings.Contains(l.print("F"), "AddWrap(") {
		t.Fatalf("the counter still wraps:\n%s", l.print("F"))
	}
}

func TestIndexCheckExplicit(t *testing.T) {
	l := lowerSrc(t, `A(xs: []u8, i: i32): u8 {
	return xs[i]
}
B(xs: []u8, i: u16): u8 {
	return xs[i]
}
C(xs: []u8, i: int): u8 {
	return xs[i]
}
D(xs: []u8, i: uint): u8 {
	return xs[i]
}
`)
	wantIn(t, l.print("A"), "Lossless[int](copy _2)", "Trunc[uint]", "Lt(", "panic bounds")
	wantIn(t, l.print("B"), "Lossless[uint](copy _2)", "Lt(", "panic bounds")
	wantIn(t, l.print("C"), "Trunc[uint](copy _2)", "Lt(", "panic bounds")
	if d := l.print("D"); strings.Contains(d, "[uint](copy _2)") {
		t.Fatalf("a uint index is cast:\n%s", d)
	}
}

func TestTrapsAreImplicit(t *testing.T) {
	l := lowerSrc(t, `F(a: int, b: int): int {
	return -(a + b - a * b / b % b << 1 >> 1)
}
`)
	f := l.print("F")
	wantAll(t, f, "Add(", "Sub(", "Mul(", "Div(", "Rem(", "Shl(", "Shr(", "Neg(")
	if strings.Contains(f, "panic") {
		t.Fatalf("an implicit trap got an explicit check:\n%s", f)
	}
}

func TestMust(t *testing.T) {
	l := lowerSrc(t, `error Denied
Load(id: int): Denied!int {
	return id
}
Next(): ?int {
	return none
}
F(): int {
	return Next().Must() + Load(1).Must()
}
`)
	wantAll(t, l.print("F"), "Discriminant(", "panic must", "err must _", "panic must copy _")
}

func TestAssertAndPanic(t *testing.T) {
	l := lowerSrc(t, `F(c: bool) {
	Assert(c)
	if !c {
		Panic("no")
	}
}
`)
	wantIn(t, l.print("F"), "switch copy _1 [0: ", "panic assert", "panic explicit ")
}

func TestCastLosslessByType(t *testing.T) {
	l := lowerSrc(t, `A(x: i32): i64 {
	return Cast[i64](x)
}
B(x: i32): i8 {
	return Cast[i8](x)
}
`)
	wantIn(t, l.print("A"), "Lossless[i64]")
	wantIn(t, l.print("B"), "Checked[i8]")
}

func TestOrOrder(t *testing.T) {
	l := lowerSrc(t, `Next(): ?int {
	return none
}
Fallback(): int {
	return 1
}
F(): int {
	return Next() else { Fallback() }
}
`)
	wantIn(t, l.print("F"), "call m.Next()", "Discriminant(", "call m.Fallback()")
}

func TestScopeJoin(t *testing.T) {
	l := lowerSrc(t, `Work(io: Io) {
}
F(io: Io, n: int) {
	scope s := io.Scope() {
		s.go Work(io)
		if n > 1 {
			return
		}
	}
}
`)
	f := l.print("F")
	wantIn(t, f, "call std.core.Io:Scope(", "spawn _", "join _")
	if !strings.Contains(f, " cancel") || count(f, "join _") < 2 {
		t.Fatalf("want a cancelling join on the early return and a plain one at the end:\n%s", f)
	}
}

func TestContracts_CallsPredicates(t *testing.T) {
	l := lowerSrc(t, `SafeDiv(a: int, b: int): int
	requires b != 0
	ensures result <= a
{
	return a / b
}
struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
Make(): Span {
	s := Span(lo: 1, hi: 4)
	return s
}
`)
	wantIn(t, l.print("SafeDiv"), "#requires0", "Div(", "#ensures0", "return normal", "panic contract")
	wantIn(t, l.print("Make"), "m.Span(", "#invariant0", "panic contract")
}

// TestContractsReceiverFields: a requires or ensures that reads a field of
// a &self or mut self receiver, or calls a method on it, lowers and
// validates. The predicate's &P parameter is a reference to a reference, and
// the member access must deref both (a property test found it; it was an
// E0000).
func TestContractsReceiverFields(t *testing.T) {
	l := lowerSrc(t, `struct Span {
	lo: int
	hi: int
}
Span.Len(&self): int {
	return self.hi - self.lo
}
Span.Swap(mut self)
	requires self.hi < 1000 && self.Len() >= 0
{
	t := self.lo
	self.lo = self.hi
	self.hi = t
}
Span.Get(&self): int
	requires self.hi < 1000 && self.Len() >= 0
	ensures result == self.lo
{
	return self.lo
}
`)
	wantIn(t, l.print("Span:Swap"), "#requires0", "panic contract")
	wantIn(t, l.print("Span:Get"), "#requires0", "#ensures0", "panic contract")
}

func TestFieldsLoop_Generic(t *testing.T) {
	l := lowerSrc(t, `Dump[A: Fields[Format]](w: mut Writer, args: A): WriteError!void {
	for name, value in fields(&args) {
		try w.Str(name)
		try value.Format(w)
	}
}
`)
	b := l.body("Dump")
	if len(b.Loops) != 1 || b.Loops[0].FieldsOf == 0 || b.Loops[0].FieldsParam != 1 {
		t.Fatalf("loop info %+v", b.Loops)
	}
	f := l.print("Dump")
	wantAll(t, f, "FieldName(0)", "= &_2.loop0", "Format.method0[$1]")
	if strings.Contains(f, "(*_2)") {
		t.Fatalf("the value place derefs args:\n%s", f)
	}
}

func TestTypeTest(t *testing.T) {
	l := lowerSrc(t, `Memchr(xs: []u8, x: u8): int {
	return 0
}
IndexOf[T: Eq](xs: []T, x: &T): int {
	if T is u8 {
		return Memchr(xs, *x)
	}
	return 1
}
`)
	wantIn(t, l.print("IndexOf"), "typetest $0 is u8", "Narrow[[]u8](copy _1)", "Narrow[&u8](copy _2)", "call m.Memchr(")
}

func TestDestructure(t *testing.T) {
	l := lowerSrc(t, `struct Point {
	x: int
	y: int
}
F(p: Point, r: (a: int, b: int)): int {
	Point(x, y) := p
	(a, b) := r
	return x + y + a + b
}
`)
	wantIn(t, l.print("F"), "= copy _1", ".0", ".1", "= copy _2")
}

func TestMatch_BoolPatterns(t *testing.T) {
	l := lowerSrc(t, `F(b: bool): int {
	match b {
		true {
			return 1
		}
		false {
			return 0
		}
	}
}
`)
	f := l.print("F")
	// One switch on the value, complete (no default).
	wantAll(t, f, "switch copy _2 [1: bb2, 0: bb3]")
	if strings.Contains(f, "// true") {
		t.Fatalf("true bound as a local:\n%s", f)
	}
}
