// Why: the corpus judge must tell which pipeline stages this build of mk
// runs, and which diagnostic phase it reaches, to know whether a unit's
// expected verdict can be judged at all.
//
// What: Stage and its names, StageSet (Has, With, Stages), Available and
// Implemented.
//
// How: one bit per stage. Every stage is wired, so Available is the full set
// and Implemented is PhaseFlow; the corpus then allows no pending unit.

package driver

import "mk/internal/diag"

// Stage names the pipeline stages, in order.
type Stage uint8

const (
	StageNone Stage = iota
	StageManifest
	StageLoad
	StageParse
	StageResolve
	StageGen
	StageSignatures
	StageBodies
	StageFlow
	StageIface
	StageInstances
	StageCodegen
	StageNative
	numStages
)

var stageNames = [numStages]string{"none", "manifest", "load", "parse", "resolve", "gen", "signatures",
	"bodies", "flow", "iface", "instances", "codegen", "native"}

// String is the stage's lower-case name ("resolve").
func (s Stage) String() string {
	if s < numStages {
		return stageNames[s]
	}
	return "stage?"
}

// StageSet is a set of stages, one bit per Stage.
type StageSet uint32

// Has reports whether s holds st.
func (s StageSet) Has(st Stage) bool { return s&(1<<st) != 0 }

// With returns s with st added.
func (s StageSet) With(st ...Stage) StageSet {
	for _, x := range st {
		s |= 1 << x
	}
	return s
}

// Stages lists the members of s in Stage order.
func (s StageSet) Stages() []Stage {
	var out []Stage
	for st := StageNone + 1; st < numStages; st++ {
		if s.Has(st) {
			out = append(out, st)
		}
	}
	return out
}

// m4Stages is every stage: the manifest step, loading, parsing, resolution,
// signatures and bodies (check, lower and the evaluator), flow (borrow), the
// instances (mono: the instance-limit walk of mk check and corpus units,
// walk.go, and build's collection), codegen and native (clang and the link,
// native.go), gen (stage 4, gen.go) and iface (the module, function and
// object caches over internal/iface and internal/cache: incremental.go,
// frontcache.go, funccache.go).
var m4Stages = StageSet(0).With(StageManifest, StageLoad, StageParse, StageResolve, StageGen,
	StageSignatures, StageBodies, StageFlow, StageIface, StageInstances, StageCodegen, StageNative)

// Available is the set of stages wired into this build of mk. A stage is in
// it when this build runs it, not when every program gets through it: a
// native build that stops on another package's internal error is an E0000
// at that stage, never a stage left out of this set. It holds every stage,
// so the corpus allows no pending unit.
//
// StageInstances is wired: Session.Check and CheckUnit run mono's walk over
// the limit roots after stage 7. A corpus session whose Through is types
// walks its units too, with stage 7 run silently for borrow's guard
// decisions (world.noFlow). StageIface is wired for every session with a
// cache (mk check, build, run, test); --no-cache runs the same stages with
// it off, which changes no output (TestNoCacheEqualsCached).
func Available() StageSet { return m4Stages }

// Implemented is the last diagnostic phase whose stages are wired into this
// build of mk: PhaseNames once resolve runs, PhaseTypes once check and
// lower run, PhaseFlow once borrow runs, as it does. It says nothing
// about std; CorpusSession.Through combines both.
func Implemented() diag.Phase { return diag.PhaseFlow }
