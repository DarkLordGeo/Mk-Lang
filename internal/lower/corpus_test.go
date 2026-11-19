// Why: the MIR validator must pass on every lowered body of the corpus.
// What: TestCorpusLowersAndValidates and its helpers (corpusUnits,
// lowerUnitsJudged, whyUnbuilt, noEvaluator, noGenerator).
// How: every verdict-ok block of doc/*.md and the programs fizz.mk and wc.mk
// are checked with the corpus prelude, and every unit check accepts is
// lowered with Validate on.

package lower

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
)

// corpusUnit is one source judged by the corpus test.
type corpusUnit struct {
	key, src string
	bin      bool
}

func corpusUnits(t testing.TB) []corpusUnit {
	keys, texts := docBlocks(t)
	var out []corpusUnit
	for _, k := range keys {
		if strings.HasPrefix(texts[k], "// verdict: ok\n") {
			out = append(out, corpusUnit{key: k, src: texts[k]})
		}
	}
	for _, p := range []string{"fizz.mk", "wc.mk"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "tests", "programs", p))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, corpusUnit{key: "programs/" + p, src: strings.ReplaceAll(string(data), "\r\n", "\n"), bin: true})
	}
	return out
}

// lowerUnitsOf checks and lowers every unit of b that check accepts; it
// returns the bodies by key and the ICE messages.
func lowerUnitsOf(w *world, b *built) (map[string]*mir.Body, []string) {
	return lowerUnitsWith(w, b, false)
}

// lowerUnitsWith is lowerUnitsOf with the trim pass on or off.
func lowerUnitsWith(w *world, b *built, trim bool) (map[string]*mir.Body, []string) {
	bodies, ices, _ := lowerUnitsJudged(w, b, trim)
	return bodies, ices
}

// lowerUnitsJudged is lowerUnitsWith that also returns the units it did not
// lower because stage 6 rejected them: one line per unit, its item key and
// its first diagnostic (or the check ICE). Lower's precondition is a body
// check accepted, so these are not lowered; a caller judging a
// verdict-ok source must count them, not drop them.
func lowerUnitsJudged(w *world, b *built, trim bool) (bodies map[string]*mir.Body, ices, rejected []string) {
	cfg := w.lowerCfg()
	cfg.Trim = trim
	bodies = map[string]*mir.Body{}
	for _, id := range b.m.Units() {
		it := w.items.Get(id)
		var body *check.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil || body == nil {
			what := "check returned no body"
			if ie != nil {
				what = "check ICE: " + ie.What
			}
			rejected = append(rejected, fmt.Sprintf("%s: %s", it.Key, what))
			continue
		}
		if len(body.Diags) > 0 {
			d := body.Diags[0]
			rejected = append(rejected, fmt.Sprintf("%s at %s: %v %s (%d diagnostics)", it.Key, w.lc(d.At), d.Code, d.Message, len(body.Diags)))
			continue
		}
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { mb = Lower(cfg, FromCheck(cfg, w.c, body)) }); ie != nil {
			ices = append(ices, fmt.Sprintf("%s at %s: %s", it.Key, w.lc(ie.Span), ie.What))
			continue
		}
		bodies[it.Key.String()] = mb
	}
	return bodies, ices, rejected
}

// corpusWorld builds one corpus unit as module m.
func corpusWorld(t testing.TB, core, pre string, u corpusUnit) (*world, *built, bool) {
	w := newWorldCore(t, true, core)
	added := map[string]bool{}
	var addStub func(path string)
	addStub = func(path string) {
		if added[path] {
			return
		}
		added[path] = true
		for _, d := range stubDeps[path] {
			addStub(d)
		}
		w.add(path, opt{std: true}, stdStubs[path])
	}
	for _, path := range stubPaths() {
		if imports(u.src, path) {
			addStub(path)
		}
	}
	// A doc block is judged as a module listed in unsafe_modules.
	o := opt{parseErrors: true, bin: u.bin, unsafe: !u.bin}
	if !u.bin {
		o.testPrelude = pre
	}
	var b *built
	if ie := source.Guard("module", source.Span{}, func() { b = w.add("m", o, u.src) }); ie != nil || b == nil || b.m == nil {
		return w, nil, false
	}
	if len(b.pdiags) > 0 || len(b.missing) > 0 {
		return w, b, false // a parse error or an import the world lacks: not check-accepted input
	}
	for _, d := range b.diags() {
		if d.Code != diag.E0208 {
			return w, b, false
		}
	}
	return w, b, true
}

// imports reports whether src has an import line of path, plain or renamed
// (`import t = std.text`), with or without a trailing comment.
func imports(src, path string) bool {
	for _, l := range strings.Split(src, "\n") {
		if !strings.HasPrefix(l, "import ") {
			continue
		}
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		if f := strings.Fields(l); f[len(f)-1] == path {
			return true
		}
	}
	return false
}

// unbuiltExcepted are the verdict-ok blocks this harness cannot build, each
// with why: they need a generator or more of the real std than a stub
// should copy. mk corpus judges each one. An entry that builds, or that
// names no verdict-ok block, fails the test, so the list stays current.
var unbuiltExcepted = map[string]string{
	"examples.md:109":    "the log store opens its file through fs.ReadWrite, Options and OpenWith, the real std.fs's options API",
	"examples.md:260":    "gen dyn.Of writes the DeviceDyn type the block uses; this harness runs no generator",
	"overview.md:670":    "gen dyn.Of writes the type the block uses; this harness runs no generator",
	"overview.md:1302":   "gen derive.Eq, derive.Hash and json.Codec; this harness runs no generator and has no std.derive",
	"reference.md:7529":  "gen derive.Eq; this harness runs no generator and has no std.derive",
	"reference.md:7556":  "a generator function: Gen's Block, Str and Fields are std's generator API, which this harness does not declare",
	"reference.md:7663":  "a generator function: Gen's Block, Str and Fields are std's generator API, which this harness does not declare",
	"reference.md:10839": "gen derive.Eq; this harness runs no generator and has no std.derive",
}

// TestCorpusLowersAndValidates: every unit of every verdict-ok block lowers
// with no ICE and passes Validate (Config.Validate). A verdict-ok source
// the harness cannot build (a parse error, a missing import, a stage-5
// diagnostic) or a body check rejects is an error too, not a skip: the
// harness's fake prelude and std stubs must carry what the block uses, or
// "every lowered body of the corpus" silently shrinks (PageSize was once
// dropped this way, by a fake TargetInfo with no fields).
func TestCorpusLowersAndValidates(t *testing.T) {
	core, pre := corpusPrelude(t), testPrelude(t)
	units := corpusUnits(t)
	blocks, lowered := 0, 0
	var ices, unbuilt, rejected []string
	srcOf := map[string]string{}
	for _, u := range units {
		srcOf[u.key] = u.src
		w, b, ok := corpusWorld(t, core, pre, u)
		if !ok {
			unbuilt = append(unbuilt, u.key+": "+whyUnbuilt(w, b))
			continue
		}
		blocks++
		bodies, is, rs := lowerUnitsJudged(w, b, false)
		lowered += len(bodies)
		for _, s := range is {
			ices = append(ices, u.key+": "+s)
		}
		for _, s := range rs {
			rejected = append(rejected, u.key+": "+s)
		}
	}
	sort.Strings(ices)
	t.Logf("%d units (%d not built), %d bodies lowered and validated, %d rejected by check, %d ICEs", blocks, len(unbuilt), lowered, len(rejected), len(ices))
	seen := map[string]bool{}
	for _, s := range unbuilt {
		if noEvaluator(s) {
			t.Logf("no evaluator in this harness (mk corpus judges it): %s", s)
			continue
		}
		k := s[:strings.Index(s, ": ")]
		if why, ok := unbuiltExcepted[k]; ok {
			seen[k] = true
			t.Logf("not built here, %s: %s", why, s)
			continue
		}
		t.Errorf("verdict-ok source not built by the harness: %s", s)
	}
	for k, why := range unbuiltExcepted {
		if _, ok := srcOf[k]; !ok {
			t.Errorf("unbuiltExcepted names %s, which is not a verdict-ok block", k)
		} else if !seen[k] {
			t.Errorf("%s builds now: remove it from unbuiltExcepted (%s)", k, why)
		}
	}
	for _, s := range rejected {
		if noEvaluator(s) {
			t.Logf("no evaluator in this harness (mk corpus judges it): %s", s)
			continue
		}
		if noGenerator(srcOf[s[:strings.Index(s, ": ")]], s) {
			t.Logf("no generator in this harness (mk corpus judges it): %s", s)
			continue
		}
		t.Errorf("verdict-ok body rejected by check, so never lowered: %s", s)
	}
	for _, s := range ices {
		if corpusPending(s) {
			t.Logf("pending: %s", s)
			continue
		}
		t.Errorf("%s", s)
	}
}

// whyUnbuilt is the first reason corpusWorld refused a source.
func whyUnbuilt(w *world, b *built) string {
	switch {
	case b == nil || b.m == nil:
		return "the module did not build"
	case len(b.pdiags) > 0:
		return fmt.Sprintf("parse %v %s", b.pdiags[0].Code, b.pdiags[0].Message)
	case len(b.missing) > 0:
		return "missing import " + b.missing[0]
	}
	for _, d := range b.diags() {
		if d.Code != diag.E0208 {
			return fmt.Sprintf("%v at %s: %s", d.Code, w.lc(d.At), d.Message)
		}
	}
	return "unknown"
}

// noEvaluator reports whether a harness line is check's E0901 for want of
// an evaluator: this harness has none, so a block whose types need a
// compile-time requires (std.simd's lane counts) is judged by mk
// corpus, which runs one.
func noEvaluator(s string) bool {
	return strings.Contains(s, "E0901") && strings.Contains(s, "needs an evaluator, and this build has none")
}

// genRe matches a gen line of a block and captures the type it generates for.
var genRe = regexp.MustCompile(`(?m)^gen [A-Za-z_][A-Za-z0-9_.]*\(([A-Za-z_][A-Za-z0-9_]*)`)

// noGenerator reports whether a harness line is check's want of a gen's
// output: this harness runs no generator (gen needs the evaluator), so a
// member or impl that a `gen` line of src emits for its type (json.Codec's
// FromJson and Json, regex.Matcher's IsMatch, cli.Args' Parse) is missing,
// and mk corpus, which runs the generators, judges the block. Only E0203
// and E0701 naming that very type count, so any other failure still fails.
func noGenerator(src, s string) bool {
	for _, m := range genRe.FindAllStringSubmatch(src, -1) {
		name := "`" + m[1] + "`"
		if strings.Contains(s, "E0203 "+name+" has no member ") || strings.Contains(s, "E0701 "+name+" does not satisfy ") {
			return true
		}
	}
	return false
}

// corpusPending lists the ICE tags of lowerings not built yet; an ICE that
// carries one is counted apart from lower's own.
func corpusPending(s string) bool {
	for _, p := range []string{"(request L-1)", "(request L-8)", "(check gap)"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
