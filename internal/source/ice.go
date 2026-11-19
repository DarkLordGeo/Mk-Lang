// Why: a compiler bug must never reach the user as a Go stack trace; it must
// become one diagnostic that says where it happened.
//
// What: ICE, ICEf and Assertf (raising), InternalError and Guard (catching).
//
// How: ICEf panics with an *ICE; Guard recovers any panic in f, an ICE or a
// runtime error, into an InternalError, which callers report as E0000
// internal-error with the facts where and what.

package source

import (
	"fmt"
	"runtime/debug"
)

// ICE is the typed panic value of an internal compiler error: a violated
// invariant of the compiler, never an error in the user's program (those are
// diagnostics). Raise one with ICEf or Assertf; Guard recovers it. Span is the
// best-known location of the offending construct, or the zero Span.
type ICE struct {
	Span Span
	Msg  string
}

// Error renders the message, so an ICE also satisfies the error interface.
func (e *ICE) Error() string { return "internal compiler error: " + e.Msg }

// ICEf panics with an *ICE carrying span and the formatted message. It is the
// single raise site of internal errors; a grep for "ICEf(" finds them all.
func ICEf(span Span, format string, args ...any) {
	panic(&ICE{Span: span, Msg: fmt.Sprintf(format, args...)})
}

// Assertf raises an ICE (as ICEf) when cond is false.
func Assertf(cond bool, span Span, format string, args ...any) {
	if !cond {
		panic(&ICE{Span: span, Msg: fmt.Sprintf(format, args...)})
	}
}

// InternalError is what Guard returns when its unit of work panicked. The
// caller turns it into an E0000 internal-error diagnostic, with Where as
// facts.where and What as facts.what, and the process exits 3. Where, Span and
// What are deterministic for a given input; Stack is not.
type InternalError struct {
	// Where names the unit of work, as the caller passed it to Guard: an item's
	// stable name, a file path, a pack key.
	Where string
	// Span is the ICE's span when it had one, else the span passed to Guard.
	Span Span
	// What is the ICE's message, or the text of any other panic value.
	What string
	// Runtime is true when the panic was not an ICE: a Go runtime error (nil
	// dereference, index out of range) or any other panic value.
	Runtime bool
	// Stack is the goroutine's stack at the panic, for a debug log only. It
	// holds addresses and goroutine numbers, so it never reaches diagnostics,
	// JSON or the corpus.
	Stack string
}

// String renders "internal compiler error in <where>: <what>".
func (e *InternalError) String() string {
	return "internal compiler error in " + e.Where + ": " + e.What
}

// Guard runs f and recovers any panic it raises, returning nil when f returned
// normally and an *InternalError otherwise. Every top-level unit of work (a file
// parse, an item check, a function body, a codegen pack) and every worker
// goroutine body runs under Guard, so one broken item never crashes the
// compiler or hides the diagnostics of others. A fatal Go error (stack
// exhaustion, out of memory, concurrent map writes) is not a panic and cannot be
// recovered; recursion depth limits exist to keep it from happening.
func Guard(where string, span Span, f func()) (ie *InternalError) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		ie = &InternalError{Where: where, Span: span, Stack: string(debug.Stack())}
		switch v := r.(type) {
		case *ICE:
			ie.What = v.Msg
			if v.Span.File != NoFile {
				ie.Span = v.Span
			}
		case ICE:
			ie.What = v.Msg
			if v.Span.File != NoFile {
				ie.Span = v.Span
			}
		case error:
			ie.What, ie.Runtime = v.Error(), true
		default:
			ie.What, ie.Runtime = fmt.Sprint(v), true
		}
	}()
	f()
	return nil
}
