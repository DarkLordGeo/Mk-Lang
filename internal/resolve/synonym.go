// Why: programmers from other languages reach for names like Vec, usize,
// Rc or .unwrap; E1221 names the Mk spelling and fixes it, and resolve and
// check must agree on one table.
//
// What: Synonym (one row), the table synonyms, Synonyms (a copy for check)
// and synonymFor.
//
// How: the table is sorted bytewise by Foreign. Name rows apply here,
// member rows in check; the macro forms (println!, format!) and `dyn I` are
// the parser's.

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
}

// synonyms is the E1221 table, sorted bytewise by Foreign. The
// macro forms (println!, format!) and `dyn I` are the parser's.
var synonyms = [...]Synonym{
	{Foreign: ".clone", MkName: ".Dup", Member: true},
	{Foreign: ".expect", MkName: ".Must", Member: true},
	{Foreign: ".to_string", MkName: ".Fmt", Member: true},
	{Foreign: ".unwrap", MkName: ".Must", Rename: true, Member: true},
	{Foreign: "Arc", MkName: "sync.Shared", Import: "import std.sync", Rename: true},
	{Foreign: "HashMap", MkName: "Map", Rename: true},
	{Foreign: "Rc", MkName: "sync.Shared", Import: "import std.sync", Rename: true},
	{Foreign: "String", MkName: "Text", Rename: true},
	{Foreign: "Vec", MkName: "List", Rename: true},
	{Foreign: "fmt.Println", MkName: "Print"},
	{Foreign: "usize", MkName: "int", Rename: true},
}

// Synonyms is the table, sorted by Foreign. Names (not members) are applied
// here; member rows are check's. The result is a fresh copy.
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
