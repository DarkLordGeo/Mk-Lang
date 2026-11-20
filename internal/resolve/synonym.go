// Why: programmers from other languages reach for names like Vec, usize,
// Rc or .unwrap; E1221 names the Mk spelling and fixes it, and resolve and
// check must agree on one table.
//
// What: Synonym (one row), the table synonyms, Synonyms (a copy for check),
// synonymFor and MemberSynonym.
//
// How: the table is sorted bytewise by Foreign. Name rows apply here;
// member rows apply in check, after a member lookup failed
// (MemberSynonym). The macro forms (println!, format!) and `dyn I` are the
// parser's.

package resolve

// Synonym is one row of the E1221 synonym table.
type Synonym struct {
	Foreign string // "Vec", "usize", "Rc", "fmt.Println", ".unwrap"
	MkName  string // "List", "int", "sync.Shared", "Print", ".Must"
	Import  string // "import std.sync" when MkName needs one, else ""
	// Rename is true when replacing the token is the whole fix (proven).
	Rename bool
	// Member is true for a member row (".unwrap"), which check applies.
	Member bool
	// OnFallible is true for a member row that applies only to a `?T` or
	// `E!T` value, the types whose member MkName is.
	OnFallible bool
	// Note, when set, is the message's Mk form in place of MkName's.
	Note string
}

// synonyms is the E1221 table, sorted bytewise by Foreign. A member row
// whose MkName starts with '.' names a member; the others name a call.
var synonyms = [...]Synonym{
	{Foreign: ".clone", MkName: "try arena.Dup(s)", Member: true, Note: "a plain value copies as it is, and text copies with `try arena.Dup(s)`"},
	{Foreign: ".expect", MkName: ".Must", Member: true, OnFallible: true},
	{Foreign: ".to_string", MkName: "try arena.Fmt(\"{x}\", (x))", Member: true},
	{Foreign: ".unwrap", MkName: ".Must", Rename: true, Member: true, OnFallible: true},
	{Foreign: "Arc", MkName: "sync.Shared", Import: "import std.sync", Rename: true},
	{Foreign: "HashMap", MkName: "Map", Rename: true},
	{Foreign: "Rc", MkName: "sync.Shared", Import: "import std.sync", Rename: true},
	{Foreign: "String", MkName: "Text", Rename: true},
	{Foreign: "Vec", MkName: "List", Rename: true},
	{Foreign: "fmt.Println", MkName: "Print"},
	{Foreign: "usize", MkName: "int", Rename: true},
}

// Synonyms is the table, sorted by Foreign. The result is a fresh copy.
func Synonyms() []Synonym { return append([]Synonym(nil), synonyms[:]...) }

// synonymFor finds the name row (not a member row) for a spelling.
func synonymFor(foreign string) (Synonym, bool) {
	for _, s := range synonyms {
		if !s.Member && s.Foreign == foreign {
			return s, true
		}
	}
	return Synonym{}, false
}

// MemberSynonym finds the member row for a member name written without its
// dot ("unwrap").
func MemberSynonym(name string) (Synonym, bool) {
	for _, s := range synonyms {
		if s.Member && s.Foreign[1:] == name {
			return s, true
		}
	}
	return Synonym{}, false
}
