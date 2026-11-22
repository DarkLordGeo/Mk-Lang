// Why: the borrow checker's soundness is tested by running many random
// programs it accepts and checking that none reads a dead value; that needs
// a generator whose programs are valid by construction and reproducible from
// a seed.
//
// What: GenVersion, Profile, SoundProgram, GenerateSound and
// GenerateSoundTwin, ProgramSeed, and the generator sgen with its random
// source, name pools, helpers, blocks, endings and the entry Run. The
// shapes themselves are in soundshapes.go and its siblings.
//
// How: a program is one module: the header items it uses
// (tests/soundness/header.mk), the consts S30 needs, up to three helpers,
// and the entry Run. Everything is i64 with wrapping arithmetic, every
// index is in range by construction and every loop is bounded, so no run
// traps or runs out of fuel, and the evaluator, a dev build and a release
// build compute the same Run(k). Randomness is splitmix64 only.

package corpus

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// GenVersion names the generator's output: a change that changes the text
// of any (profile, seed) bumps it, so a record's seed still reproduces.
// What each version added (a profile not named keeps its text but for the
// version line):
//
//   - 2: the clause draws come from a stream of their own, so a program's
//     clause-restored twin (GenerateSoundTwin) has the same body; the move
//     and init shapes S35 to S38 for E0401 to E0406 and E0409; spawns
//     counted per run through enclosing loops (at most 64).
//   - 3: S39 (temporaries, E0506) and S40 (reborrowing, E0507); a helper's
//     &i64 result one time in 12 a reference to its own local (E0603); the
//     ending reads a ref that S10 borrowed only through its holder (the
//     transitive liveness step); a consuming helper takes only an arena of
//     the calling block (version 2 could move one on one path of a branch:
//     E0402, 229 defects in 2,383,522 programs).
//   - 4: S41 (reassigning a reference kills its loans), S42 (an arena that
//     holds a loan on itself, Reset or freed while read) and S20's cell that
//     keeps every element (disjoint elements).
//   - 5: one consuming S34 helper in two takes n: i64 after bp, passed
//     k ^ <site>, so the caller's arena goes through an argument slot
//     (M-R13-order).
//   - 6: S43, a sub-arena passed by value through an argument slot to the
//     header's Drop (version 5's form was too rare: no program of 20,000
//     was dropped by M-R13-order).
//   - 7: profiles lang and vec (and oracle and reject where no io or arena
//     is needed): capturing literals S44 to S47, fn values of generic
//     instances S48, Arena.Fmt's short form S49, simd.Vec values S50 to S54,
//     and M-E0411's lever: a badFree helper fails right after bp.Free(),
//     with no random draw.
//   - 8: profiles m7 and reject7 (and oracle, without the io and arena
//     shapes): value arms in every value position through ref and ival,
//     diverging arms and else value blocks on ?T and E!T (S55, S56), for
//     over an iterator with a destructured payload (S57), index receivers
//     (S58), Swap and Replace (S59), SplitAt and GetPair (S60), ChunksMut
//     under a scope with tasks writing disjoint pieces (S61) and mem.Pool's
//     GetPair and Iter (S62).
//   - 9: reject7 calls SplitAt, GetPair, Chunks and ChunksMut through the
//     header's non-generic wrappers, so the rejection sample's Rejected runs reach them
//     (version 8 left 4,561 of 20,000 sampled programs Skipped poisoned).
//   - 10: in m7, reject7 and oracle, S65 (value arms over a scrutinee that
//     holds a loan or a temporary), S66 (Cr.Keep, into self, through an
//     index, a nested index, a List and a helper's mut []Cr parameter), S67
//     (for over an iterator whose payload borrows the hidden var, and over a
//     named one), S68 (arms that do not fall through in call arguments,
//     constructor fields, array elements and compound assignments, and as
//     Drop's argument after its arena) and S69 (references through the
//     Slice[T] views and in mem.Pool[&i64]).
//   - 11: the per-field stratum (soundfields.go) in profiles fields (m7 with
//     S70 to S81) and rejectf (reject7 with them).
//   - 12: the header's FhPut no longer writes x.b through x.a: that write is
//     judged in FhPut's own body (E0604 x into x), which no clause can
//     declare, so the header was rejected. FhPut now only stores x into l,
//     and the caller's self-reach and call rules are what S77 draws.
//   - 13: the hole strata S82 to S88 (soundholes.go) in profile fields only:
//     later-hop stores into an allocator, self-reach through a forwarding
//     parameter, W2's crossed per-field result, shallow and deep stores into
//     one holder, and a global reached through a call, each in a function
//     of its own, with the header's callees StashL to KkPoke.
//   - 14: a hole function writes its hole one time in three (holeOdds 3, was
//     2).
//   - 15: S89 (soundreset.go) in profiles full and fields only: an arena
//     made over a sub-arena's Alloc (Arena.NewIn, the header's NewIn, or
//     the Sub of such an arena) reset, then a cell, List, mem.Pool, str or
//     sub-arena made in it and read, with the backing reset first one time
//     in three, in a function of its own.
const GenVersion = 15

// Profile selects the shapes a program may use.
type Profile uint8

const (
	ProfileFull    Profile = iota // every shape of this file's generator: the default of a run
	ProfileCore                   // no tasks, no Io, no arena: runs in the plain evaluator too
	ProfileTasks                  // Full with the task shapes weighted up
	ProfileReject                 // Full with no onerr (the rejection sample)
	ProfileOracle                 // Core: no shape whose value depends on the schedule
	ProfileLang                   // capturing literals, F[T] values, short Print
	ProfileVec                    // simd.Vec values
	ProfileViews                  // vec with the value-arm, iterator and view shapes (S55 to S69)
	ProfileReject7                // reject with the S55 to S69 shapes (the rejection sample on that language)
	ProfileFields                 // field-level loan sets: m7 with the per-field stratum (soundfields.go)
	ProfileRejectF                // reject7 with the per-field stratum (the rejection sample with the stratum included)
	nProfiles
)

var profileNames = [nProfiles]string{"full", "core", "tasks", "reject", "oracle", "lang", "vec", "m7", "reject7", "fields", "rejectf"}

// String is the profile's name as MK_SOUND_PROFILE spells it.
func (p Profile) String() string {
	if p < nProfiles {
		return profileNames[p]
	}
	return "profile" + strconv.Itoa(int(p))
}

// ParseProfile is String's inverse.
func ParseProfile(s string) (Profile, bool) {
	for i, n := range profileNames {
		if n == s {
			return Profile(i), true
		}
	}
	return 0, false
}

// SoundProgram is one generated program: a single module's text and what
// the runner needs to call it.
type SoundProgram struct {
	Version int
	Profile Profile
	Seed    uint64
	Text    string   // the module, header included
	Entry   string   // "Run"
	Params  []string // the entry's parameter kinds in order: "k", "arena", "io"
	Shapes  []uint16 // the shape IDs emitted, sorted, for coverage counts
	Lines   int
	// Dropped is the number of clauses the clause draws left out (a store
	// clause, or a from clause made incomplete); 0 in a twin.
	Dropped int
	// Twin: the clause-restored twin of the program of the same seed
	// (GenerateSoundTwin): every clause drawn is written.
	Twin bool
	// Leak: S37 left a sub-arena unfreed on one path on purpose, so an
	// E0402 is a rejection by the linear-value rule, not a generator defect.
	Leak bool
	// CapMisuse: S47 wrote a capture form check rejects in the types group
	// (E0609) on purpose, so such an E0609 is a rejection by the capture
	// restrictions, not a generator defect.
	CapMisuse bool
}

// splitmix64 is one step of splitmix64 from state x: the mixing function
// the run seeds and the program seeds of a run are drawn with.
func splitmix64(x uint64) uint64 {
	s := splitmix{state: x}
	return s.next()
}

// RunSeeds is the run seeds of program seed: n values k_j = splitmix64(seed
// ^ (j+1)·0x9e3779b97f4a7c15), each both the entry's k and the TagOptions
// Seed.
func RunSeeds(seed uint64, n int) []uint64 {
	out := make([]uint64, n)
	for j := range out {
		out[j] = splitmix64(seed ^ (uint64(j+1) * 0x9e3779b97f4a7c15))
	}
	return out
}

// ProgramSeed is the seed of program i of a run with base b.
func ProgramSeed(base uint64, i int) uint64 { return splitmix64(base + uint64(i)) }

// kind is what a pooled name holds.
type kind uint8

const (
	kInt    kind = iota // i64
	kRef                // &i64
	kMut                // mut i64
	kRefRef             // mut &i64
	kCell               // Cell[&i64] (a param: &Cell[&i64])
	kDeep               // Deep (a param: mut Deep)
	kD2                 // D2 (a param: &D2)
	kSl                 // Sl
	kLx                 // Lx
	kArray              // [4]i64
	kSlice              // []i64
	kMSlice             // mut []i64
	kMutex              // Mutex[&i64]
	kArena              // a local sub-arena (linear)
	kACell              // mut Cell[&i64] carved from an arena
	kStr                // a str from an arena's Fmt (S49)
	kVec                // simd.Vec[i64, 2] (S50 to S53)
	kCts                // [2]Ct, whose elements take mut receivers (S58)
	kPool               // mem.Pool[i64] (S62)
	kCrs                // [2]Cr, whose Keep stores into an element (S66; a helper's param: mut []Cr)
	kGrs                // [2]Gr: Keep through a nested index (S66)
	kCrList             // List[Cr] of two: Keep through ItemsMut (S66)
	kRArr               // [4]&i64, written through SplitAt, GetPair and ChunksMut views (S69)
	kPoolR              // mem.Pool[&i64] (S69)
	kFp                 // Fp, a struct of two reference fields, split (S70)
	kFq                 // Fq, an Fp nested in a struct with a reference field (S70)
	kFw                 // Fw, a struct with a Cell inline: never split past &w (S70)
	kFa                 // Fa, a holder of an arena cell beside a reference field (S76)
	kFh                 // Fh, a self-reach holder of an Fs (S77)
	nKinds
)

// sname is one name in scope.
type sname struct {
	name  string
	kind  kind
	param bool // a helper's parameter, already a reference to its referent
	fixed bool // not a var: never reassigned, never borrowed mut
	// moved: S35 moved it out, or S38 freed it early; no shape picks it
	// and no ending folds it.
	moved bool
	// loop is the number of loops around its declaration: a move of a
	// name declared outside the innermost loop is E0405 (S35).
	loop int
	// inQ: S10 borrowed this ref mut into a q, which the ending reads
	// through, so the ending does not read it directly: its loan then
	// stays live only through its holder q (liveness is transitive).
	inQ  bool
	used bool // read since its declaration (the ending folds the rest)
	n    int  // a slice local's length; 0 when unknown (a parameter)
	// deferred: an arena whose Free a defer runs (S32), so neither the
	// ending nor an else handler frees it.
	deferred bool
}

// origin is where a reference value came from inside a helper: a
// parameter itself, or something read through one (for its clauses).
type origin struct {
	p       *sname
	through bool
}

// sblock is one open block: the pool lengths on entry and the names it
// declared, in order.
type sblock struct {
	saved [nKinds]int
	decls []*sname
}

// shelper is one generated helper.
type shelper struct {
	name    string
	params  []*sname
	usesK   bool // takes var k: i64 first
	assignK bool
	refRes  bool     // the result is &i64; else i64
	from    []string // the from clause, when refRes
	// arena: S34's form, Hn(a: &Arena, ..): OutOfMemory!i64 with a
	// sub-arena under onerr and a later try step; only Run calls it.
	arena bool
	// consumes: S34's form also takes bp: Arena by value and frees it,
	// after its last failure point, or (badFree) before its first, which
	// the checker rejects (E0411: the caller keeps bp on the error edge).
	consumes, badFree bool
	// late (GenVersion 5): a consuming helper's n: i64 right after bp,
	// passed k ^ <site>, an operand that is not inert, so lower passes bp
	// through an argument slot read in evaluation order, and the caller's
	// arena is restored through that slot on the error edge (M-R13-order).
	late bool
	// stores are the flows the body makes; declared are those the
	// signature states, which a caller helper restates.
	stores   []hstore
	declared []hstore
	// into is the parameter flow written on each direct &i64 parameter.
	into map[*sname]*sname
	body string
	sig  string
}

type hstore struct {
	from    *sname
	through bool
	to      *sname
}

// sgen is one program's generator.
type sgen struct {
	rng     splitmix
	crng    splitmix // the clause draws, a stream of their own
	twin    bool     // every clause drawn is written (GenerateSoundTwin)
	dropped int
	leak    bool
	loops   int // loops around the statement being written
	mult    int // the product of their trip-count bounds
	dyn     int // spawns per run through the enclosing loops (at most 64)
	prof    Profile
	seed    uint64
	n       int // the fresh-name counter of the function being written
	site    int // the k ^ site counter
	pools   [nKinds][]*sname
	blocks  []sblock
	b       strings.Builder // the function being written
	ind     int
	depth   int
	shapes  [nShapes + 1]bool
	items   [nItems]bool
	consts  []string // header-level const and var lines, in order
	statics []string // the const names a static loan may take
	global  string   // S31's global mutex, once declared
	hs      []*shelper
	cur     *shelper // the helper being written, nil in Run
	// Run's parameters and facts.
	usesA, usesIO, assignK bool
	spawns                 int
	hUsed                  bool // the function's h was read
	capMisuse              bool // S47 wrote a types-group E0609 form
	simd                   bool // a vec shape was written: import std.simd
	mem                    bool // a pool shape was written: import std.mem
	// armDepth is the value arms being written around the current draw
	// (GenVersion 8): ref and ival write a value arm only below 2.
	armDepth int
	// fhRefs is, per self-reach holder (S77), the length of
	// the reference pool before it: FhPut borrows only those.
	fhRefs map[*sname]int
	// fgen are S81's generated per-field helpers, written after the consts.
	fgen []string
	// hgen are the hole strata's generated functions (S82 to S88),
	// written after fgen; holeN counts them per prefix.
	hgen  []string
	holeN map[string]int
}

// GenerateSound returns program seed of profile p. It is a pure function of
// (GenVersion, p, seed): no map order, no clock, no math/rand. It is not
// named Generate because corpus.Generate is the compile-speed package's.
func GenerateSound(p Profile, seed uint64) SoundProgram { return generateSound(p, seed, false) }

// GenerateSoundTwin is GenerateSound with every drawn clause written:
// the program of the same (p, seed) with its helpers' missing clauses
// restored. The clause draws use their own stream, so the twin's text
// differs from the program's only on the helpers' signature lines (so the
// rejection sample can separate "the signature lacks a clause" from the rule).
func GenerateSoundTwin(p Profile, seed uint64) SoundProgram { return generateSound(p, seed, true) }

func generateSound(p Profile, seed uint64, twin bool) SoundProgram {
	g := &sgen{rng: splitmix{state: seed ^ 0x536f756e64476e31}, prof: p, seed: seed, // "SoundGn1"
		crng: splitmix{state: seed ^ 0x436c617573655332}, twin: twin, mult: 1} // "ClauseS2"
	nh := g.rng.intn(4)
	for i := range nh {
		g.helper(i + 1)
	}
	run := g.run()
	var out strings.Builder
	fmt.Fprintf(&out, "// Generated by corpus.GenerateSound: version %d, profile %s, seed %#016x.\n", GenVersion, p, seed)
	if g.simd {
		out.WriteString("\nimport std.simd\n")
	}
	if g.mem {
		out.WriteString("\nimport std.mem\n")
	}
	for i := range nItems {
		if g.items[i] {
			out.WriteByte('\n')
			out.WriteString(headerItems()[i].text)
		}
	}
	if len(g.consts) > 0 {
		out.WriteByte('\n')
		for _, c := range g.consts {
			out.WriteString(c)
			out.WriteByte('\n')
		}
	}
	for _, f := range g.fgen {
		out.WriteByte('\n')
		out.WriteString(f)
	}
	for _, f := range g.hgen {
		out.WriteByte('\n')
		out.WriteString(f)
	}
	for _, h := range g.hs {
		out.WriteByte('\n')
		out.WriteString(h.sig)
		out.WriteString(" {\n")
		out.WriteString(h.body)
		out.WriteString("}\n")
	}
	out.WriteByte('\n')
	out.WriteString(run)
	text := out.String()
	prog := SoundProgram{Version: GenVersion, Profile: p, Seed: seed, Text: text, Entry: "Run", Params: []string{"k"}, Lines: strings.Count(text, "\n"),
		Dropped: g.dropped, Twin: twin, Leak: g.leak, CapMisuse: g.capMisuse}
	if g.usesA {
		prog.Params = append(prog.Params, "arena")
	}
	if g.usesIO {
		prog.Params = append(prog.Params, "io")
	}
	for id := 1; id <= nShapes; id++ {
		if g.shapes[id] {
			prog.Shapes = append(prog.Shapes, uint16(id))
		}
	}
	return prog
}

// fresh returns a new name with the given prefix.
func (g *sgen) fresh(prefix string) string {
	g.n++
	return prefix + strconv.Itoa(g.n)
}

// line writes one line of the function at the current indentation.
func (g *sgen) line(s string) {
	for range g.ind {
		g.b.WriteString("    ")
	}
	g.b.WriteString(s)
	g.b.WriteByte('\n')
}

func (g *sgen) linef(format string, args ...any) { g.line(fmt.Sprintf(format, args...)) }

// lit is a small i64 literal.
func (g *sgen) lit() string { return strconv.Itoa(g.rng.intn(100)) }

// chance is true with probability num/den.
func (g *sgen) chance(num, den int) bool { return g.rng.intn(den) < num }

// declare adds a fresh name of kind k to the current block.
func (g *sgen) declare(k kind, prefix string) *sname { return g.declareNamed(k, g.fresh(prefix)) }

// declareNamed adds the name to the current block's pools and decls.
func (g *sgen) declareNamed(k kind, name string) *sname {
	s := &sname{name: name, kind: k, loop: g.loops}
	g.pools[k] = append(g.pools[k], s)
	if n := len(g.blocks); n > 0 {
		g.blocks[n-1].decls = append(g.blocks[n-1].decls, s)
	}
	return s
}

// pick returns a random name of kind k (a var one when needVar), or nil.
func (g *sgen) pick(k kind, needVar bool) *sname {
	p := g.pools[k]
	if !needVar && !g.anyMoved(k) {
		if len(p) == 0 {
			return nil
		}
		return p[g.rng.intn(len(p))]
	}
	n := 0
	for _, s := range p {
		if (!needVar || !s.fixed) && !s.moved {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	j := g.rng.intn(n)
	for _, s := range p {
		if (!needVar || !s.fixed) && !s.moved {
			if j == 0 {
				return s
			}
			j--
		}
	}
	return nil
}

// anyMoved reports whether a name of kind k in scope was moved out (only
// S35's Sl and S38's arenas are).
func (g *sgen) anyMoved(k kind) bool {
	if k != kSl && k != kArena {
		return false
	}
	for _, s := range g.pools[k] {
		if s.moved {
			return true
		}
	}
	return false
}

func (g *sgen) has(k kind, needVar bool) bool {
	for _, s := range g.pools[k] {
		if (!needVar || !s.fixed) && !s.moved {
			return true
		}
	}
	return false
}

// open starts a block: its pools are restored when it closes.
func (g *sgen) open() {
	var b sblock
	for k := range nKinds {
		b.saved[k] = len(g.pools[k])
	}
	g.blocks = append(g.blocks, b)
	g.depth++
	g.ind++
}

// close runs the block's ending and restores the pools.
func (g *sgen) close() {
	b := &g.blocks[len(g.blocks)-1]
	g.ending(b.decls)
	for k := range nKinds {
		g.pools[k] = g.pools[k][:b.saved[k]]
	}
	g.blocks = g.blocks[:len(g.blocks)-1]
	g.depth--
	g.ind--
}

// fold writes h = h *% 31 +% e.
func (g *sgen) fold(e string) {
	g.hUsed = true
	g.line("h = h *% 31 +% " + e)
}

// ending folds every name the block declared into h, latest first, so a
// later holder's loan ends before the name it holds is read. A
// mut name, a reference to a reference and a slice are folded only when
// nothing read them yet: reading them again would only extend their loans.
func (g *sgen) ending(decls []*sname) {
	for i := len(decls) - 1; i >= 0; i-- {
		s := decls[i]
		if s.moved {
			s.used = true
			continue
		}
		switch s.kind {
		case kInt:
			g.fold(s.name)
		case kRef:
			if !s.inQ {
				g.fold("*" + s.name)
			}
		case kMut:
			if !s.used {
				g.fold("*" + s.name)
			}
		case kRefRef:
			if !s.used {
				g.fold("**" + s.name)
			}
		case kCell, kACell:
			g.fold("*" + s.name + ".Load()")
		case kDeep, kD2:
			g.fold("*" + s.name + ".a")
		case kSl:
			g.fold("*" + s.name + ".m")
		case kLx:
			g.fold("*" + s.name + ".src +% " + s.name + ".pos")
		case kArray:
			g.fold(fmt.Sprintf("%[1]s[0] +%% %[1]s[1] +%% %[1]s[2] +%% %[1]s[3]", s.name))
		case kSlice, kMSlice:
			if !s.used {
				g.fold(s.name + "[0]")
			}
		case kMutex:
			g.fold("*" + s.name + ".Into()")
		case kStr:
			g.fold("i64(" + s.name + ".Len())")
		case kVec:
			g.fold(s.name + ".ReduceSumWrap()")
		case kCts:
			g.fold(s.name + "[0].n +% " + s.name + "[1].n")
		case kPool:
			g.fold("i64(" + s.name + ".Len())")
		case kCrs, kCrList:
			g.fold("*" + s.name + "[0].r +% *" + s.name + "[1].r")
		case kGrs:
			g.fold(fmt.Sprintf("*%[1]s[0].rows[0].r +%% *%[1]s[0].rows[1].r +%% *%[1]s[1].rows[0].r +%% *%[1]s[1].rows[1].r", s.name))
		case kRArr:
			g.fold(fmt.Sprintf("*%[1]s[0] +%% *%[1]s[1] +%% *%[1]s[2] +%% *%[1]s[3]", s.name))
		case kPoolR:
			hd, vl := g.fresh("hd"), g.fresh("vl")
			g.linef("for (handle: %s, value: %s) in %s.Iter() {", hd, vl, s.name)
			g.linef("    _ = %s", hd)
			g.linef("    h = h *%% 31 +%% **%s", vl)
			g.line("}")
			g.hUsed = true
		case kArena:
			if !s.deferred {
				g.linef("%s.Free()", s.name)
			}
		case kFp, kFq, kFw, kFa, kFh:
			g.fold(fieldsEnding(s))
		}
		s.used = true
	}
}

// branch writes the k step and the condition of a branch on it.
func (g *sgen) branch() string {
	if g.cur != nil {
		g.cur.usesK, g.cur.assignK = true, true
	} else {
		g.assignK = true
	}
	g.line("k = k *% 6364136223846793005 +% 1442695040888963407")
	return "((k >> 33) & 1) == 1"
}

// kArg is the k argument of a call that takes var k: i64 first.
func (g *sgen) kArg() string {
	if g.cur != nil {
		g.cur.usesK = true
	}
	g.site++
	return "k ^ " + strconv.Itoa(g.site)
}

// use marks a header item as emitted.
func (g *sgen) use(it int) {
	g.items[it] = true
	for _, d := range headerItems()[it].deps {
		g.items[d] = true
	}
}

// ref draws a <ref> and its origins. In Run:
// two chances in &v of an int local, then one each for a ref, Id(r),
// Pick(k, r, &v), and with cells c.Load() and Hold(&c). In a helper the
// same forms over the parameters, plus *q, d.a and e.a.
func (g *sgen) ref() (string, []origin) {
	if g.armOK() && g.chance(1, 5) {
		g.shapes[sArmRef] = true
		return g.armRef()
	}
	type choice struct {
		k  kind
		op int
	}
	var cs []choice
	if g.has(kInt, false) {
		cs = append(cs, choice{kInt, 0}, choice{kInt, 0})
	}
	if g.has(kRef, false) {
		cs = append(cs, choice{kRef, 0}, choice{kRef, 1}, choice{kRef, 2})
	}
	if g.has(kCell, false) {
		cs = append(cs, choice{kCell, 0}, choice{kCell, 1})
	}
	if g.has(kACell, false) {
		cs = append(cs, choice{kACell, 0})
	}
	if g.cur != nil {
		if g.has(kRefRef, false) {
			cs = append(cs, choice{kRefRef, 0})
		}
		if g.has(kDeep, false) {
			cs = append(cs, choice{kDeep, 0})
		}
		if g.has(kD2, false) {
			cs = append(cs, choice{kD2, 0})
		}
	}
	if g.cur == nil {
		// GenVersion 11: a field of a split local (only in the stratum's
		// profiles, whose pools alone hold these kinds).
		if g.has(kFp, false) {
			cs = append(cs, choice{kFp, 0}, choice{kFp, 1})
		}
		if g.has(kFq, false) {
			cs = append(cs, choice{kFq, 0})
		}
	}
	if len(cs) == 0 {
		return "", nil
	}
	c := cs[g.rng.intn(len(cs))]
	s := g.pick(c.k, false)
	s.used = true
	org := func(through bool) []origin {
		if s.param {
			return []origin{{s, through}}
		}
		return nil
	}
	switch c.k {
	case kInt:
		return "&" + s.name, nil
	case kRef:
		switch c.op {
		case 0:
			return s.name, org(false)
		case 1:
			g.use(itemId)
			return "Id(" + s.name + ")", org(false)
		}
		g.use(itemPick)
		other, o2 := g.ref()
		return "Pick(" + g.kArg() + ", " + s.name + ", " + other + ")", append(org(false), o2...)
	case kCell:
		if c.op == 0 {
			return s.name + ".Load()", org(true)
		}
		g.use(itemHold)
		if s.param {
			return "Hold(" + s.name + ")", org(true)
		}
		return "Hold(&" + s.name + ")", nil
	case kACell:
		return s.name + ".Load()", nil
	case kFp:
		return s.name + [2]string{".a", ".b"}[c.op], nil
	case kFq:
		// The field c is written `"." + "c"`: TestNoCSource reads a
		// literal ending in .c as a C file passed to a tool.
		return s.name + [3]string{".p.a", ".p.b", "." + "c"}[g.rng.intn(3)], nil
	case kRefRef:
		return "*" + s.name, org(true)
	}
	return s.name + ".a", org(true) // kDeep, kD2: a helper's parameter
}

// stmts draws n statements at the current depth.
func (g *sgen) stmts(n int) {
	for range n {
		g.stmt()
	}
}

// helper writes helper Hi: its parameters, its body over the
// parameter pools, the clauses its stores and its result need.
func (g *sgen) helper(i int) {
	h := &shelper{name: "H" + strconv.Itoa(i), into: map[*sname]*sname{}}
	g.cur, g.n, g.site, g.hUsed = h, 0, 0, false
	g.b.Reset()
	g.ind = 1
	g.depth = 0
	g.pools = [nKinds][]*sname{}
	g.blocks = []sblock{{}}
	np := 1 + g.rng.intn(4)
	pk := []kind{kRef, kMut, kRefRef, kCell, kDeep, kD2, kSlice, kMSlice}
	if viewsProfile(g.prof) {
		pk = append(pk, kCrs) // a mut []Cr whose elements Keep stores into
	}
	prefix := map[kind]string{kRef: "r", kMut: "m", kRefRef: "q", kCell: "c", kDeep: "dp", kD2: "e", kSlice: "z", kMSlice: "z", kCrs: "cs"}
	for range np {
		k := pk[g.rng.intn(len(pk))]
		if k == kCrs {
			g.use(itemCrKeep)
		}
		s := &sname{name: g.fresh(prefix[k]), kind: k, param: true, fixed: true}
		h.params = append(h.params, s)
		g.pools[k] = append(g.pools[k], s)
		switch k {
		case kDeep:
			g.use(itemDeep)
		case kD2:
			g.use(itemD2)
		}
	}
	// The result: &i64 when some parameter can supply one; S34's form
	// returns OutOfMemory!i64.
	var res string
	var resOrg []origin
	if shapeWeight(sOnerr, g.prof) > 0 && g.chance(1, 5) {
		h.arena = true
		g.shapes[sOnerr] = true
		g.hUsed = true
		if g.chance(1, 3) {
			h.consumes = true
			if g.chance(1, 4) {
				h.badFree = true
				g.line("bp.Free()")
				// M-E0411's lever: a failure point that always
				// fails without taking a page (over MaxBytes / 8, so
				// OutOfMemory), so every run of a badFree helper takes the
				// error edge after bp.Free(). No random draw.
				g.line("zq := try a.Slice[i64](2305843009213693952)")
				g.line("h = h *% 31 +% i64(zq.Len())")
			}
			if g.chance(1, 2) {
				h.late = true
				g.line("h = h *% 31 +% n")
			}
		}
		g.line("b := try a.Sub()")
		g.line("onerr b.Free()")
	} else if g.chance(1, 2) && (g.has(kRef, false) || g.has(kCell, false) || g.has(kRefRef, false) || g.has(kDeep, false) || g.has(kD2, false)) {
		h.refRes = true
	}
	nb := 1 + g.rng.intn(6)
	for range nb {
		g.hstmt()
	}
	if h.arena {
		g.line("t := try b.Sub()")
		g.line("t.Free()")
		g.line("b.Free()")
		if g.chance(1, 2) {
			// A failure point after b is consumed: the onerr guard must skip
			// the onerr there (M-R21-onerr runs it: a second Free).
			g.line("u := try a.Sub()")
			g.line("u.Free()")
		}
		if h.consumes && !h.badFree {
			g.line("bp.Free()")
		}
	}
	// Every parameter is used (E0208).
	for _, s := range h.params {
		if !s.used {
			g.useParam(s)
		}
	}
	if h.refRes {
		res, resOrg = g.ref()
		if g.chance(1, 12) {
			// Result sources at the boundary: a reference to the helper's own
			// local, which dies at the return (E0603).
			res, resOrg, g.hUsed = "&h", nil, true
		}
	}
	body := g.b.String()
	var b strings.Builder
	if g.hUsed {
		b.WriteString("    var h: i64 = 0\n")
	}
	b.WriteString(body)
	if h.refRes {
		fmt.Fprintf(&b, "    return %s\n", res)
	} else {
		b.WriteString("    return h\n")
		if !g.hUsed {
			b.Reset()
			b.WriteString("    var h: i64 = 0\n")
			b.WriteString(body)
			b.WriteString("    return h\n")
		}
	}
	h.body = b.String()
	h.sig = g.signature(h, resOrg)
	g.hs = append(g.hs, h)
	g.cur = nil
}

// useParam reads a parameter nothing used yet.
func (g *sgen) useParam(s *sname) {
	s.used = true
	switch s.kind {
	case kRef:
		g.fold("*" + s.name)
	case kMut:
		g.fold("*" + s.name)
	case kRefRef:
		g.fold("**" + s.name)
	case kCell:
		g.fold("*" + s.name + ".Load()")
	case kDeep, kD2:
		g.fold("*" + s.name + ".a")
	case kSlice, kMSlice:
		g.fold("i64(" + s.name + ".Len())")
	case kCrs:
		g.fold("*" + s.name + "[0].r")
	}
}

// store records that a value of origins org lands in parameter to (a
// store into memory reached from it), for the clauses.
func (g *sgen) store(org []origin, to *sname) {
	h := g.cur
	if h == nil || to == nil || !to.param {
		return
	}
	for _, o := range org {
		h.stores = append(h.stores, hstore{from: o.p, through: o.through, to: to})
	}
}

// signature spells helper h's signature with its clauses: each needed
// store clause written with probability 0.85, and the from clause of a
// &i64 result complete with probability 0.85, so the missing
// share gives the programs E0602 and E0604 reject.
func (g *sgen) signature(h *shelper, resOrg []origin) string {
	var star []string
	seen := map[string]bool{}
	for _, st := range h.stores {
		if st.through || st.from.kind != kRef {
			key := "*" + st.from.name + " into " + st.to.name
			if !seen[key] {
				seen[key] = true
				if g.clause() {
					star = append(star, key)
					h.declared = append(h.declared, hstore{from: st.from, through: true, to: st.to})
				} else {
					g.dropped++
				}
			}
			continue
		}
		if _, ok := h.into[st.from]; ok {
			continue // one into per parameter; a second target goes unstated
		}
		if g.clause() {
			h.into[st.from] = st.to
			h.declared = append(h.declared, hstore{from: st.from, to: st.to})
		} else {
			h.into[st.from] = nil
			g.dropped++
		}
	}
	var b strings.Builder
	b.WriteString(h.name)
	b.WriteByte('(')
	first := true
	if h.usesK {
		if h.assignK {
			b.WriteString("var ")
		}
		b.WriteString("k: i64")
		first = false
	}
	if h.arena {
		if !first {
			b.WriteString(", ")
		}
		b.WriteString("a: &Arena")
		if h.consumes {
			b.WriteString(", bp: Arena")
		}
		if h.late {
			b.WriteString(", n: i64")
		}
		first = false
	}
	for _, p := range h.params {
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(p.name)
		b.WriteString(": ")
		b.WriteString(paramType[p.kind])
		if to := h.into[p]; to != nil {
			b.WriteString(" into ")
			b.WriteString(to.name)
		}
	}
	b.WriteByte(')')
	var flows []string
	if h.refRes {
		b.WriteString(": &i64")
		var src []string
		for _, p := range h.params {
			for _, o := range resOrg {
				if o.p == p && !slices.Contains(src, p.name) {
					src = append(src, p.name)
				}
			}
		}
		if len(src) == 0 {
			src = []string{h.params[0].name}
		}
		if !g.clause() {
			// The incomplete answer: one source fewer, or another one.
			if len(src) >= 2 {
				src = src[1:]
				g.dropped++
			} else if len(h.params) >= 2 {
				g.dropped++
				for _, p := range h.params {
					if p.name != src[0] {
						src = []string{p.name}
						break
					}
				}
			}
		}
		flows = append(flows, "from "+strings.Join(src, ", "))
		h.from = src
	} else if h.arena {
		b.WriteString(": OutOfMemory!i64")
	} else {
		b.WriteString(": i64")
	}
	flows = append(flows, star...)
	if len(flows) > 0 {
		b.WriteByte(' ')
		b.WriteString(strings.Join(flows, ", "))
	}
	return b.String()
}

// clause is one clause draw: kept with probability 0.85, from the
// clause stream; in a twin always kept (the draw is still made, so the
// stream stays aligned with the program's).
func (g *sgen) clause() bool {
	keep := g.crng.intn(100) < 85
	return keep || g.twin
}

// paramType is a helper parameter's type by kind.
var paramType = [nKinds]string{
	kRef: "&i64", kMut: "mut i64", kRefRef: "mut &i64", kCell: "&Cell[&i64]",
	kDeep: "mut Deep", kD2: "&D2", kSlice: "[]i64", kMSlice: "mut []i64", kCrs: "mut []Cr",
}

// run writes the entry Run.
func (g *sgen) run() string {
	g.cur, g.n, g.hUsed = nil, 0, true
	g.b.Reset()
	g.ind = 1
	g.depth = 0
	g.pools = [nKinds][]*sname{}
	g.blocks = []sblock{{}}
	g.line("var h: i64 = 0")
	n := 3 + g.rng.intn(12)
	g.stmts(n)
	g.ending(g.blocks[0].decls)
	g.line("h = h *% 31 +% k")
	g.line("return h")
	body := g.b.String()
	var b strings.Builder
	b.WriteString("export Run(")
	if g.assignK {
		b.WriteString("var ")
	}
	b.WriteString("k: i64")
	if g.usesA {
		b.WriteString(", a: &Arena")
	}
	if g.usesIO {
		b.WriteString(", io: Io")
	}
	b.WriteString("): i64 {\n")
	b.WriteString(body)
	b.WriteString("}\n")
	return b.String()
}
