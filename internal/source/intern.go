// Why: every name in the compiler is compared and hashed often; interning
// makes a name one small integer, and the names the language predeclares
// get fixed numbers so they can be tested without a lookup.
//
// What: Symbol, the predeclared symbols (contextual words, universe names,
// primitive types, names rules key on), IsContextual, IsUniverse,
// IsPrimitiveType, and Interner (Intern, InternBytes, Lookup, Str).
//
// How: the Interner's index is sharded by FNV-1a of the name, each shard
// behind its own lock, with strings stored in an AppendList so reads take no
// lock. Lookup is exact and case-sensitive.

package source

import (
	"strings"
	"sync"
)

// Symbol is an interned identifier. The zero value NoSymbol means "none" and
// spells the empty string. Two symbols of one Interner are equal exactly when
// their strings are equal (interning is exact and case-sensitive; the
// resolver's case-folding fallback is never done here).
// Symbol numbers follow intern order, which follows goroutine timing, so output
// is never ordered by them and they are never fed to a Hasher; the predeclared
// symbols below are the exception, fixed in every Interner.
type Symbol uint32

// NoSymbol is the zero Symbol: no name. Intern("") returns it and Str(NoSymbol) is "".
const NoSymbol Symbol = 0

// IsValid reports whether s is not NoSymbol.
func (s Symbol) IsValid() bool { return s != NoSymbol }

// Predeclared symbols, interned by NewInterner in this order, so their numbers
// are the same in every Interner and every run. The list is append-only: a new
// name goes at the end, before symPredeclaredEnd, and never renumbers another.
// Groups are contiguous so IsContextual, IsUniverse and IsPrimitiveType are range
// checks.
const (
	_ Symbol = iota // NoSymbol, ""

	// SymUnderscore is "_", the discard.
	SymUnderscore

	// Contextual words: words that are names everywhere except in
	// the one position that gives them meaning.

	SymFrom     // "from"
	SymInto     // "into"
	SymEnds     // "ends"
	SymFuel     // "fuel"
	SymPacked   // "packed"
	SymSelf     // "self", the receiver value
	SymSelfType // "Self", the receiver type
	SymResult   // "result", the result in an ensures clause

	// Universe names, the built-in bounds and EvalArena. Declaring one is
	// E0102, except ok and err, which are pattern heads only and may be
	// declared.

	SymTrue        // "true"
	SymFalse       // "false"
	SymSome        // "some"
	SymNone        // "none"
	SymOk          // "ok"
	SymErr         // "err"
	SymFields      // "fields", the compile-time loop word
	SymAssert      // "Assert"
	SymPanic       // "Panic"
	SymCast        // "Cast"
	SymTrunc       // "Trunc"
	SymSizeOf      // "SizeOf"
	SymAlignOf     // "AlignOf"
	SymTarget      // "Target"
	SymPlain       // "Plain", built-in bound
	SymSend        // "Send", built-in bound
	SymShare       // "Share", built-in bound
	SymFieldsBound // "Fields", built-in bound (distinct from SymFields, the loop word)
	SymEvalArena   // "EvalArena"

	// Primitive type names. They are names, not keywords;
	// declaring one is E0102.

	SymI8    // "i8"
	SymI16   // "i16"
	SymI32   // "i32"
	SymI64   // "i64"
	SymU8    // "u8"
	SymU16   // "u16"
	SymU32   // "u32"
	SymU64   // "u64"
	SymInt   // "int"
	SymUint  // "uint"
	SymF32   // "f32"
	SymF64   // "f64"
	SymBool  // "bool"
	SymChar  // "char"
	SymStr   // "str"
	SymVoid  // "void"
	SymNever // "never"

	// Names rules key on.

	SymMain     // "Main", the program entry
	SymLen      // "Len"
	SymNext     // "Next", for x := e iteration
	SymItems    // "Items", for x in &list
	SymItemsMut // "ItemsMut", for x in mut list

	symPredeclaredEnd // one past the last predeclared symbol
)

// predeclaredNames holds the strings of the predeclared symbols, indexed by Symbol.
var predeclaredNames = [symPredeclaredEnd]string{
	NoSymbol:      "",
	SymUnderscore: "_",

	SymFrom: "from", SymInto: "into", SymEnds: "ends", SymFuel: "fuel", SymPacked: "packed",
	SymSelf: "self", SymSelfType: "Self", SymResult: "result",

	SymTrue: "true", SymFalse: "false", SymSome: "some", SymNone: "none", SymOk: "ok", SymErr: "err",
	SymFields: "fields", SymAssert: "Assert", SymPanic: "Panic", SymCast: "Cast", SymTrunc: "Trunc",
	SymSizeOf: "SizeOf", SymAlignOf: "AlignOf", SymTarget: "Target", SymPlain: "Plain", SymSend: "Send",
	SymShare: "Share", SymFieldsBound: "Fields", SymEvalArena: "EvalArena",

	SymI8: "i8", SymI16: "i16", SymI32: "i32", SymI64: "i64",
	SymU8: "u8", SymU16: "u16", SymU32: "u32", SymU64: "u64",
	SymInt: "int", SymUint: "uint", SymF32: "f32", SymF64: "f64",
	SymBool: "bool", SymChar: "char", SymStr: "str", SymVoid: "void", SymNever: "never",

	SymMain: "Main", SymLen: "Len", SymNext: "Next", SymItems: "Items", SymItemsMut: "ItemsMut",
}

// NumPredeclared is the number of table slots the predeclared symbols occupy,
// slot 0 included: every Symbol below it is predeclared, and the first symbol
// an Interner hands out for a new name is Symbol(NumPredeclared).
const NumPredeclared = int(symPredeclaredEnd)

// PredeclaredName returns the string of a predeclared symbol, and false for any
// other symbol. It needs no Interner.
func PredeclaredName(s Symbol) (string, bool) {
	if s >= symPredeclaredEnd {
		return "", false
	}
	return predeclaredNames[s], true
}

// IsContextual reports whether s is one of the contextual words:
// from into ends fuel packed self Self result.
func IsContextual(s Symbol) bool { return s >= SymFrom && s <= SymResult }

// IsUniverse reports whether s is a universe name: the language's list, the
// built-in bounds Plain Send Share Fields, and EvalArena.
func IsUniverse(s Symbol) bool { return s >= SymTrue && s <= SymEvalArena }

// IsPrimitiveType reports whether s names a primitive type:
// i8 .. u64, int, uint, f32, f64, bool, char, str, void, never.
func IsPrimitiveType(s Symbol) bool { return s >= SymI8 && s <= SymNever }

// internShards is the number of independently locked lookup shards.
const internShards = 64

// internShard is one lock-protected slice of the string-to-symbol index.
type internShard struct {
	mu sync.RWMutex
	m  map[string]Symbol
	_  [40]byte // keeps neighbouring shards' locks off one cache line
}

// Interner maps identifier strings to Symbols and back. It is safe for
// concurrent use: Intern and Lookup lock one of 64 shards; Str takes no lock.
// It is append-only: a Symbol, once handed out, names the same string forever.
// Every Interner starts with the predeclared symbols at their fixed numbers. The
// zero Interner is not usable; call NewInterner.
type Interner struct {
	shards   [internShards]internShard
	appendMu sync.Mutex
	strs     AppendList[string]
}

// NewInterner returns an Interner holding exactly the predeclared symbols.
func NewInterner() *Interner {
	in := &Interner{}
	for i := range in.shards {
		in.shards[i].m = make(map[string]Symbol)
	}
	in.strs.Append("") // NoSymbol
	for want := SymUnderscore; want < symPredeclaredEnd; want++ {
		if got := in.Intern(predeclaredNames[want]); got != want {
			ICEf(Span{}, "predeclared symbol %q got %d, want %d", predeclaredNames[want], got, want)
		}
	}
	return in
}

// shardOf picks a shard by FNV-1a over the name's bytes. The choice affects only
// locking, never any Symbol a caller can observe in output.
func shardOf[S string | []byte](s S) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h % internShards
}

// Intern returns the symbol for s, adding it if it is new. Intern("") is
// NoSymbol. The stored string is a copy, so s may be a slice of a larger text.
func (in *Interner) Intern(s string) Symbol {
	if s == "" {
		return NoSymbol
	}
	sh := &in.shards[shardOf(s)]
	sh.mu.RLock()
	sym, ok := sh.m[s]
	sh.mu.RUnlock()
	if ok {
		return sym
	}
	return in.insert(sh, strings.Clone(s))
}

// InternBytes is Intern for a byte slice; it allocates only when the name is new.
func (in *Interner) InternBytes(b []byte) Symbol {
	if len(b) == 0 {
		return NoSymbol
	}
	sh := &in.shards[shardOf(b)]
	sh.mu.RLock()
	sym, ok := sh.m[string(b)]
	sh.mu.RUnlock()
	if ok {
		return sym
	}
	return in.insert(sh, string(b))
}

// insert adds s under the shard's write lock, unless another goroutine added
// it first. Lock order: shard, then appendMu.
func (in *Interner) insert(sh *internShard, s string) Symbol {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sym, ok := sh.m[s]; ok {
		return sym
	}
	in.appendMu.Lock()
	sym := Symbol(in.strs.Append(s))
	in.appendMu.Unlock()
	sh.m[s] = sym
	return sym
}

// Lookup returns the symbol for s without adding it; false when s was never
// interned. Lookup("") is (NoSymbol, true).
func (in *Interner) Lookup(s string) (Symbol, bool) {
	if s == "" {
		return NoSymbol, true
	}
	sh := &in.shards[shardOf(s)]
	sh.mu.RLock()
	sym, ok := sh.m[s]
	sh.mu.RUnlock()
	return sym, ok
}

// Str returns the string of sym; Str(NoSymbol) is "". A symbol this Interner
// never handed out is an ICE.
func (in *Interner) Str(sym Symbol) string {
	s, ok := in.strs.Get(uint32(sym))
	if !ok {
		ICEf(Span{}, "Interner.Str: unknown symbol %d", sym)
	}
	return s
}

// Len is the number of table slots in use, slot 0 included: the valid symbols
// are Symbol(1) .. Symbol(Len()-1).
func (in *Interner) Len() int { return int(in.strs.Len()) }
