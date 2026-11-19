// Why: an instance's key, hash and symbol must not depend on the order in
// which types and consts were interned, or incremental builds would miss
// their cache.
//
// What: keyOf (an instance's identity), symbolOf (its LLVM symbol) and
// nameOf (its readable spelling).
//
// How: a generic instance is named nominally, by its item key and
// iface.Hasher.InstanceName over its arguments; a fn literal is named by
// its content hash, so identical literals at different sites are one
// instance.

package mono

import (
	"encoding/hex"
	"strings"

	"mk/internal/types"
)

// keyOf is the identity of item at args. A non-generic function's Text is its
// item key; a generic instance's is the item key, "@" and the first 32 hex
// digits of iface.Hasher.GenericArgs(args). Hash is InstanceName(item key,
// args). A fn literal is keyed by its content: Text is "fn@" and the first 32
// hex digits of its mk.fn hash, Hash that hash, so identical literals at
// different sites are one instance.
func keyOf(env Env, item types.ItemID, args []types.GenericArg) Key {
	h := env.Hasher()
	it := env.Items().Get(item)
	if it.Kind == types.KindLiteral {
		fh := [32]byte(h.FnRef(types.FnRef{Item: item, Args: args}))
		return Key{Text: "fn@" + hex.EncodeToString(fh[:16]), Hash: fh}
	}
	k := Key{Hash: [32]byte(h.InstanceName(it.Key, args))}
	if len(args) == 0 {
		k.Text = it.Key.String()
		return k
	}
	ah := [32]byte(h.GenericArgs(args))
	k.Text = it.Key.String() + "@" + hex.EncodeToString(ah[:16])
	return k
}

// symbolOf is the LLVM symbol of an instance: the Mk name as written
// for an extern or export extern; "mk.fn." and 16 hex digits of the content
// hash for a fn literal (content-named); "mk." and the item key
// for a non-generic function; that and "." and the first 16 hex digits of
// Key.Hash for an instance with arguments.
func symbolOf(env Env, item types.ItemID, k Key, args []types.GenericArg) string {
	it := env.Items().Get(item)
	if fd, ok := it.Decl.(*types.FuncDecl); ok && fd.Sig.Extern {
		return env.Table().Interner().Str(it.Name)
	}
	if it.Kind == types.KindLiteral {
		return "mk.fn." + hex.EncodeToString(k.Hash[:8])
	}
	if len(args) == 0 {
		return "mk." + it.Key.String()
	}
	return "mk." + it.Key.String() + "." + hex.EncodeToString(k.Hash[:8])
}

// nameOf is the readable spelling of an instance: the item key, then its
// arguments in brackets, consts by value. Never identity.
func nameOf(env Env, item types.ItemID, args []types.GenericArg) string {
	it := env.Items().Get(item)
	if len(args) == 0 {
		return it.Key.String()
	}
	var b strings.Builder
	b.WriteString(it.Key.String())
	b.WriteByte('[')
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(env.Table().GenericArgString(a, nil))
	}
	b.WriteByte(']')
	return b.String()
}
