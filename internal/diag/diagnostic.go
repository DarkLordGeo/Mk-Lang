// Why: a diagnostic is read by people, by editors and by the corpus judge, so
// it must be plain data with a fixed shape rather than a formatted string.
//
// What: Diagnostic with its code, phase, anchor span, message, cause, facts,
// fixes and alternatives; FixClass, Fix, Edit and Cause; New and the chained
// setters; FromInternal (E0000) and Toolchain (E0001).
//
// How: New refuses a code outside the catalogue with an ICE, so codes are
// never invented. A Cause names its root by code and span, not by pointer, so
// it is valid across passes; Order resolves it to an index.

package diag

import (
	"strconv"

	"mk/internal/source"
)

// FixClass is how far a fix is trusted (see doc/tools.md).
type FixClass uint8

const (
	// Proven: behaviour preserved, checked by recompiling; mk fix applies it.
	Proven FixClass = iota
	// Compiles: checked to compile but changes meaning; offered, not applied.
	Compiles
	// Alternatives: named options, unchecked; never applied. Unchecked options
	// normally go in Diagnostic.Alternatives instead of a Fix.
	Alternatives
)

// String is the JSON spelling: proven, compiles or alternatives.
func (c FixClass) String() string {
	switch c {
	case Proven:
		return "proven"
	case Compiles:
		return "compiles"
	case Alternatives:
		return "alternatives"
	}
	return "fixclass(" + strconv.Itoa(int(c)) + ")"
}

// Edit replaces the text of Span with Text. An empty Span is an insertion at
// its start. An Edit's span lies in one file, like every span.
type Edit struct {
	Span source.Span
	Text string
}

// Fix is one suggested change: its class, the edits that make it (applied
// together, non-overlapping, in any order), and one sentence saying why.
type Fix struct {
	Class FixClass
	Edits []Edit
	Why   string
}

// Cause names the diagnostic a consequence follows from, by code and span
// so it is stateless across passes, resolved to an emission index by Order.
type Cause struct {
	Code Code
	At   source.Span
}

// Field is one key of a diagnostic's facts object. Keys are lower snake_case
// from a fixed vocabulary; their order is the order the emitter adds
// them, which is fixed per code.
type Field struct {
	Key   string
	Value Fact
}

// Diagnostic is one error in the user's program (or E0000, E0001). It is plain
// data: the JSON writer and the text renderer both read it, and nothing about
// it depends on goroutine timing. Build one with New and the chained setters;
// once it is handed to the driver it is not modified.
type Diagnostic struct {
	// Code is a catalogue code; New refuses any other.
	Code Code
	// Phase is the emitter's group, DefaultPhase(Code) unless In overrode it.
	Phase Phase
	// At is where the fix belongs: the anchor the corpus compares
	// with a `// <- Exxxx` marker line. The zero Span means no location.
	At source.Span
	// Message is one line of English, with no code or location prefix.
	Message string
	// Cause is the diagnostic this one is a consequence of, or nil when it is
	// a root cause itself.
	Cause *Cause
	// Facts are the code's structured facts, in emitter order.
	Facts []Field
	// Fixes are checked suggestions; Alternatives are named, unchecked options.
	Fixes        []Fix
	Alternatives []string
}

// New returns a diagnostic of a catalogue code at a span with a message, in
// the code's default phase. An unknown code is an ICE: codes are never
// invented.
func New(code Code, at source.Span, message string) *Diagnostic {
	if !code.IsKnown() {
		source.ICEf(at, "diag.New: %v is not in the catalogue", code)
	}
	return &Diagnostic{Code: code, Phase: DefaultPhase(code), At: at, Message: message}
}

// In sets the phase, for a code whose emitter is not its primary one
// (the emitting pass chooses the group). An invalid phase is an ICE.
func (d *Diagnostic) In(p Phase) *Diagnostic {
	if !p.IsValid() {
		source.ICEf(d.At, "diag: invalid phase %d", p)
	}
	d.Phase = p
	return d
}

// Fact appends one fact. A key already present is an ICE: facts are an object.
func (d *Diagnostic) Fact(key string, v Fact) *Diagnostic {
	for _, f := range d.Facts {
		if f.Key == key {
			source.ICEf(d.At, "diag: fact %q set twice on %v", key, d.Code)
		}
	}
	d.Facts = append(d.Facts, Field{Key: key, Value: v})
	return d
}

// Text appends a string fact.
func (d *Diagnostic) Text(key, s string) *Diagnostic { return d.Fact(key, TextFact(s)) }

// Int appends an integer fact.
func (d *Diagnostic) Int(key string, n int64) *Diagnostic { return d.Fact(key, IntFact(n)) }

// Loc appends a location fact, rendered "path:line:col".
func (d *Diagnostic) Loc(key string, at source.Span) *Diagnostic { return d.Fact(key, AtFact(at)) }

// Fix appends a fix.
func (d *Diagnostic) Fix(f Fix) *Diagnostic {
	d.Fixes = append(d.Fixes, f)
	return d
}

// Alt appends a named alternative, written as an instruction.
func (d *Diagnostic) Alt(s string) *Diagnostic {
	d.Alternatives = append(d.Alternatives, s)
	return d
}

// ConsequenceOf records that d follows from the diagnostic with this code at
// this span. Order resolves it to that diagnostic's emission index, or to d's
// own index when the cause is not emitted.
func (d *Diagnostic) ConsequenceOf(code Code, at source.Span) *Diagnostic {
	d.Cause = &Cause{Code: code, At: at}
	return d
}

// FactValue returns the fact with the key and whether it is present.
func (d *Diagnostic) FactValue(key string) (Fact, bool) {
	for _, f := range d.Facts {
		if f.Key == key {
			return f.Value, true
		}
	}
	return Fact{}, false
}

// FromInternal turns a recovered internal error into E0000 internal-error with
// the facts where and what. Its phase is PhaseInternal, so it is
// emitted whatever else fails.
func FromInternal(ie *source.InternalError) *Diagnostic {
	return New(E0000, ie.Span, ie.String()).Text("where", ie.Where).Text("what", ie.What)
}

// StderrLimit is the most bytes of a tool's error output an E0001 carries.
const StderrLimit = 2000

// Toolchain builds E0001 toolchain for a missing or failing tool: the tool's
// name, the exact command line, and its error output cut to StderrLimit bytes
// (at a UTF-8 boundary). It has no location.
func Toolchain(tool, command, stderr, message string) *Diagnostic {
	if len(stderr) > StderrLimit {
		cut := StderrLimit
		for cut > 0 && stderr[cut]&0xC0 == 0x80 {
			cut--
		}
		stderr = stderr[:cut]
	}
	return New(E0001, source.Span{}, message).Text("tool", tool).Text("command", command).Text("stderr", stderr)
}
