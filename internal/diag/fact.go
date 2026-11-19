// Why: tools act on a diagnostic's details (the name, the loan, the limit
// hit), so those details travel as typed facts, not as words in the message.
//
// What: Fact and FactKind (text, int, bool, location, code, loan, list,
// object), LoanKind, one constructor per kind, and Location.
//
// How: a Fact is one tagged struct, not an interface, so it copies as plain
// data. It renders itself to JSON and to text; a location renders as
// "path:line:col", or "-" for none.

package diag

import (
	"strconv"

	"mk/internal/source"
)

// FactKind tags the payload of a Fact.
type FactKind uint8

const (
	// FactText is a string: a name, a type or signature as Mk source.
	FactText FactKind = iota
	// FactInt is an integer: a limit, a step count, a line.
	FactInt
	// FactBool is true or false.
	FactBool
	// FactAt is a location, rendered "path:line:col" (or "-" for none).
	FactAt
	// FactCode is another diagnostic code, rendered "E0501".
	FactCode
	// FactLoan is {"place","kind","at"}: a loan, its kind and where it was made.
	FactLoan
	// FactList is an array of facts in a fixed order.
	FactList
	// FactObject is an object whose keys keep the order they were added in.
	FactObject
)

// LoanKind is the kind of a loan in a FactLoan: shared (&) or exclusive (mut).
type LoanKind uint8

const (
	// LoanShared is a & loan.
	LoanShared LoanKind = iota
	// LoanExclusive is a mut loan.
	LoanExclusive
)

// String is "shared" or "exclusive".
func (k LoanKind) String() string {
	if k == LoanExclusive {
		return "exclusive"
	}
	return "shared"
}

// Fact is one value in a diagnostic's facts: a tagged struct whose Kind says
// which payload fields are meaningful. Build one with the constructors below.
type Fact struct {
	Kind FactKind
	// Str is the FactText string, or the FactLoan place.
	Str string
	// Int is the FactInt value; Bool the FactBool value.
	Int  int64
	Bool bool
	// At is the FactAt location, or the FactLoan site.
	At source.Span
	// Code is the FactCode value.
	Code Code
	// Loan is the FactLoan kind.
	Loan LoanKind
	// List holds FactList elements, or FactObject values in key order.
	List []Fact
	// Keys holds the FactObject keys, parallel to List.
	Keys []string
}

// TextFact is a string fact.
func TextFact(s string) Fact { return Fact{Kind: FactText, Str: s} }

// IntFact is an integer fact.
func IntFact(n int64) Fact { return Fact{Kind: FactInt, Int: n} }

// BoolFact is a boolean fact.
func BoolFact(b bool) Fact { return Fact{Kind: FactBool, Bool: b} }

// AtFact is a location fact.
func AtFact(at source.Span) Fact { return Fact{Kind: FactAt, At: at} }

// CodeFact is a code fact.
func CodeFact(c Code) Fact { return Fact{Kind: FactCode, Code: c} }

// LoanFact is a loan fact: the place as written, the loan's kind,
// and the borrow site.
func LoanFact(place string, kind LoanKind, at source.Span) Fact {
	return Fact{Kind: FactLoan, Str: place, Loan: kind, At: at}
}

// ListFact is an array of facts.
func ListFact(items ...Fact) Fact { return Fact{Kind: FactList, List: items} }

// TextList is an array of strings.
func TextList(items ...string) Fact {
	l := make([]Fact, len(items))
	for i, s := range items {
		l[i] = TextFact(s)
	}
	return ListFact(l...)
}

// ObjectFact is an object; fields keep their order. A repeated key is an ICE.
func ObjectFact(fields ...Field) Fact {
	f := Fact{Kind: FactObject}
	for _, fl := range fields {
		for _, k := range f.Keys {
			if k == fl.Key {
				source.ICEf(source.Span{}, "diag: object fact key %q twice", k)
			}
		}
		f.Keys = append(f.Keys, fl.Key)
		f.List = append(f.List, fl.Value)
	}
	return f
}

// writeJSON writes the fact as a JSON value.
func (f Fact) writeJSON(w *JSONWriter, sm *source.SourceMap) {
	switch f.Kind {
	case FactText:
		w.String(f.Str)
	case FactInt:
		w.Int(f.Int)
	case FactBool:
		w.Bool(f.Bool)
	case FactAt:
		w.String(Location(sm, f.At))
	case FactCode:
		w.String(f.Code.String())
	case FactLoan:
		w.BeginObject()
		w.Key("place")
		w.String(f.Str)
		w.Key("kind")
		w.String(f.Loan.String())
		w.Key("at")
		w.String(Location(sm, f.At))
		w.EndObject()
	case FactList:
		w.BeginArray()
		for _, e := range f.List {
			e.writeJSON(w, sm)
		}
		w.EndArray()
	case FactObject:
		w.BeginObject()
		for i, k := range f.Keys {
			w.Key(k)
			f.List[i].writeJSON(w, sm)
		}
		w.EndObject()
	default:
		source.ICEf(f.At, "diag: fact kind %d", f.Kind)
	}
}

// text renders the fact for the text rendering of a diagnostic.
func (f Fact) text(sm *source.SourceMap) string {
	switch f.Kind {
	case FactText:
		return f.Str
	case FactInt:
		return strconv.FormatInt(f.Int, 10)
	case FactBool:
		return strconv.FormatBool(f.Bool)
	case FactAt:
		return Location(sm, f.At)
	case FactCode:
		return f.Code.String()
	case FactLoan:
		return f.Str + " (" + f.Loan.String() + ") at " + Location(sm, f.At)
	case FactList, FactObject:
		var w JSONWriter
		f.writeJSON(&w, sm)
		return string(w.Bytes())
	}
	source.ICEf(f.At, "diag: fact kind %d", f.Kind)
	return ""
}

// Location renders a span's start as "path:line:col", or "-" for the zero span.
func Location(sm *source.SourceMap, at source.Span) string {
	if at.File == source.NoFile || sm == nil {
		return "-"
	}
	return sm.Position(at.StartPos()).String()
}
