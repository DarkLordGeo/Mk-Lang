// Why: every cache entry is found by a key, and a key that misses an input
// returns stale results while one that includes too much makes warm builds
// miss. This file is the one place each family's key is spelled.
//
// What: BuildIDFrom, the manifest prefixes and payload keys (ParseKey,
// ModulePrefix, InterfaceKey, FuncPrefix, FuncKey, EvalPrefix, EvalKey,
// GenPrefix, GenKey), PackKey, PosKey, LibKey, LayoutKey, ImportsHash and
// ErrorCodeCollision.
//
// How: each key is the mk.cachekey hash over the entry family, then its
// fields in order, with every list sorted. Check keys hold no profile; pack
// keys hold the toolchain identity, a resolved CPU name and the rows of
// every instance.

package iface

import (
	"cmp"
	"slices"
	"strings"

	"mk/internal/source"
	"mk/internal/types"
)

// keyEnc starts a key of one family.
func keyEnc(family string) *Encoder {
	e := &Encoder{}
	e.String(family)
	return e
}

// BuildIDFrom is the build ID over the compiler executable's content hash:
// mk.build over FormatVersion and exe. It is pure: iface
// touches no disk; the driver gets exe from cache.Store.FileHash.
func BuildIDFrom(exe source.Hash) source.Hash {
	var e Encoder
	e.Uvarint(FormatVersion)
	e.Hash(exe)
	return e.sum(DomBuild)
}

// ParseKey keys a parse summary: build ID, module path, package-relative
// file path, content hash.
func ParseKey(build source.Hash, module, path string, content source.Hash) source.Hash {
	e := keyEnc("parse")
	e.Hash(build)
	e.String(module)
	e.String(path)
	e.Hash(content)
	return e.sum(DomKey)
}

// FileTokens is a file's contribution to a module prefix: token hashes only,
// never the content hash, so a comment or whitespace edit misses nothing.
// Outside is the mk.tokens hash of the file's non-trivia tokens outside items
// (imports included); Imports its mk.imports hash.
type FileTokens struct {
	Path             string
	Outside, Imports source.Hash
	Items            []ItemTokens
}

// ItemTokens is one item's token hash.
type ItemTokens struct {
	Key    types.Key
	Tokens source.Hash
}

// ImportHash is a direct import's path and part hashes.
type ImportHash struct {
	Path       string
	API, Shape source.Hash
}

// ModulePrefix is the module manifest prefix: build ID, module path, files
// sorted by path (path, outside-items token hash, imports hash, items sorted
// by key with their token hashes), direct imports sorted by path (path, API
// hash, shape hash). Never a content hash.
func ModulePrefix(build source.Hash, path string, files []FileTokens, imports []ImportHash) source.Hash {
	e := keyEnc("module")
	e.Hash(build)
	e.String(path)
	fs := slices.Clone(files)
	slices.SortFunc(fs, func(a, b FileTokens) int { return strings.Compare(a.Path, b.Path) })
	e.Uvarint(uint64(len(fs)))
	for _, f := range fs {
		e.String(f.Path)
		e.Hash(f.Outside)
		e.Hash(f.Imports)
		its := slices.Clone(f.Items)
		slices.SortFunc(its, func(a, b ItemTokens) int { return a.Key.Compare(b.Key) })
		e.Uvarint(uint64(len(its)))
		for _, it := range its {
			e.String(it.Key.String())
			e.Hash(it.Tokens)
		}
	}
	is := slices.Clone(imports)
	slices.SortFunc(is, func(a, b ImportHash) int { return strings.Compare(a.Path, b.Path) })
	e.Uvarint(uint64(len(is)))
	for _, i := range is {
		e.String(i.Path)
		e.Hash(i.API)
		e.Hash(i.Shape)
	}
	return e.sum(DomKey)
}

// fullKey is a payload key: the family, the prefix, the sorted reads.
func fullKey(family string, prefix source.Hash, reads []types.Read) source.Hash {
	e := keyEnc(family)
	e.Hash(prefix)
	writeReads(e, reads)
	return e.sum(DomKey)
}

// InterfaceKey keys a module interface: the module prefix and its external
// reads.
func InterfaceKey(prefix source.Hash, reads []types.Read) source.Hash {
	return fullKey("iface", prefix, reads)
}

// FuncPrefix is the function manifest prefix: build ID, item key, the item's
// token hash, its file's mk.imports hash. No profile.
func FuncPrefix(build source.Hash, item types.Key, tokens, imports source.Hash) source.Hash {
	e := keyEnc("funcm")
	e.Hash(build)
	e.String(item.String())
	e.Hash(tokens)
	e.Hash(imports)
	return e.sum(DomKey)
}

// FuncKey keys a function result: its prefix and its reads.
func FuncKey(prefix source.Hash, reads []types.Read) source.Hash {
	return fullKey("func", prefix, reads)
}

// EvalKeyInput is what is known about an evaluation before it runs.
type EvalKeyInput struct {
	What      types.EvalTarget
	Item      types.Key
	Args      source.Hash   // mk.args of the generic arguments
	ConstArgs []source.Hash // mk.const of each argument, in parameter order
	Fuel      uint64
	Stage     types.Stage
	Module    string // the requesting module's path
}

// EvalPrefix is the evaluation manifest prefix.
func EvalPrefix(build source.Hash, in EvalKeyInput) source.Hash {
	e := keyEnc("evalm")
	e.Hash(build)
	e.Uvarint(uint64(in.What))
	e.String(in.Item.String())
	e.Hash(in.Args)
	e.Uvarint(uint64(len(in.ConstArgs)))
	for _, c := range in.ConstArgs {
		e.Hash(c)
	}
	e.Uvarint(in.Fuel)
	e.Uvarint(uint64(in.Stage))
	e.String(in.Module)
	return e.sum(DomKey)
}

// EvalKey keys an evaluation record: its prefix and its reads.
func EvalKey(prefix source.Hash, reads []types.Read) source.Hash {
	return fullKey("eval", prefix, reads)
}

// GenKeyInput is what is known about a generator run before it runs.
type GenKeyInput struct {
	Gen, Generator types.Key
	Args           []source.Hash // per gen argument, in order
	Fuel           uint64
	Target         string
}

// GenPrefix is the generator manifest prefix.
func GenPrefix(build source.Hash, in GenKeyInput) source.Hash {
	e := keyEnc("genm")
	e.Hash(build)
	e.String(in.Gen.String())
	e.String(in.Generator.String())
	e.Uvarint(uint64(len(in.Args)))
	for _, a := range in.Args {
		e.Hash(a)
	}
	e.Uvarint(in.Fuel)
	e.String(in.Target)
	return e.sum(DomKey)
}

// GenKey keys a generator output: its prefix and its reads.
func GenKey(prefix source.Hash, reads []types.Read) source.Hash {
	return fullKey("gen", prefix, reads)
}

// PackKeyInput is everything an object pack's bytes depend on.
type PackKeyInput struct {
	Build        source.Hash
	Toolchain    source.Hash // clang version line, triple, data layout
	Target       string
	Profile      types.Profile
	CPU          string      // resolved name, never "native"
	PGO          source.Hash // release only; zero otherwise
	Instrumented bool        // --pgo-gen counters present
	Instances    []PackInstance
	Inlined      []InlinedBody // release: foreign inline candidates copied in
}

// PackInstance is one instance of a pack: its name, its content hash, and
// the hash of its rendered rows (per SpanID in ID order: package-relative
// path, line, column), in every profile.
type PackInstance struct{ Name, Content, Rows source.Hash }

// InlinedBody is an inlined body's mk.body hash and the hash of its rows.
type InlinedBody struct{ Body, Rows source.Hash }

// PackKey keys an object pack. Instances are sorted by Name and inlined
// bodies by Body (membership is a set). A CPU of "native" is an ICE: the
// driver resolves it first.
func PackKey(in PackKeyInput) source.Hash {
	if in.CPU == "native" {
		source.ICEf(source.Span{}, "iface: PackKey with an unresolved native CPU")
	}
	e := keyEnc("pack")
	e.Hash(in.Build)
	e.Hash(in.Toolchain)
	e.String(in.Target)
	e.Uvarint(uint64(in.Profile))
	e.String(in.CPU)
	e.Hash(in.PGO)
	e.Bool(in.Instrumented)
	is := slices.Clone(in.Instances)
	slices.SortFunc(is, func(a, b PackInstance) int {
		return cmp.Or(a.Name.Compare(b.Name), a.Content.Compare(b.Content), a.Rows.Compare(b.Rows))
	})
	e.Uvarint(uint64(len(is)))
	for _, i := range is {
		e.Hash(i.Name)
		e.Hash(i.Content)
		e.Hash(i.Rows)
	}
	ib := slices.Clone(in.Inlined)
	slices.SortFunc(ib, func(a, b InlinedBody) int { return cmp.Or(a.Body.Compare(b.Body), a.Rows.Compare(b.Rows)) })
	e.Uvarint(uint64(len(ib)))
	for _, i := range ib {
		e.Hash(i.Body)
		e.Hash(i.Rows)
	}
	return e.sum(DomKey)
}

// PosKey keys a positions object (codegen's positions pack, if it keeps
// one): per toolchain and target, over the rows it holds, never file
// content hashes.
func PosKey(build, toolchain source.Hash, target string, rows source.Hash) source.Hash {
	e := keyEnc("pos")
	e.Hash(build)
	e.Hash(toolchain)
	e.String(target)
	e.Hash(rows)
	return e.sum(DomKey)
}

// LibKey keys a generated import library: target and .def hash.
func LibKey(target string, defHash source.Hash) source.Hash {
	e := keyEnc("lib")
	e.String(target)
	e.Hash(defHash)
	return e.sum(DomKey)
}

// LayoutKey keys a clang data layout: clang version line and target.
func LayoutKey(clangVersion, target string) source.Hash {
	e := keyEnc("layout")
	e.String(clangVersion)
	e.String(target)
	return e.sum(DomKey)
}

// ImportsHash is mk.imports over one file's imports, (alias, module path)
// pairs in any order: sorted by alias, then path.
func ImportsHash(imports [][2]string) source.Hash {
	s := slices.Clone(imports)
	slices.SortFunc(s, func(a, b [2]string) int { return cmp.Or(strings.Compare(a[0], b[0]), strings.Compare(a[1], b[1])) })
	var e Encoder
	e.Uvarint(uint64(len(s)))
	for _, p := range s {
		e.String(p[0])
		e.String(p[1])
	}
	return e.sum(DomImports)
}

// ErrorCodeCollision reports the first two error keys, in key order, whose
// types.ErrorCode is equal; the driver raises an ICE naming both. Duplicate
// keys are not collisions.
func ErrorCodeCollision(errs []types.Key) (a, b types.Key, found bool) {
	ks := slices.Clone(errs)
	slices.SortFunc(ks, types.Key.Compare)
	ks = slices.Compact(ks)
	first := make(map[uint32]types.Key, len(ks))
	var best [2]types.Key
	for _, k := range ks {
		c := types.ErrorCode(k)
		if o, ok := first[c]; ok {
			if !found || o.Compare(best[0]) < 0 || (o == best[0] && k.Compare(best[1]) < 0) {
				best, found = [2]types.Key{o, k}, true
			}
			continue
		}
		first[c] = k
	}
	return best[0], best[1], found
}
