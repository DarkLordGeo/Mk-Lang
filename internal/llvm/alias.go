// Why: codegen knows that a slice's data does not alias other memory, but
// the ABI does not pass that data pointer as an LLVM parameter, so a
// noalias attribute cannot say it.
//
// What: the scoped alias metadata LLVM's inliner writes for a noalias
// argument: a distinct domain per function, a distinct scope per argument,
// !alias.scope and !noalias lists on the accesses, and NoAliasScopeDecl, the
// llvm.experimental.noalias.scope.decl call that marks where each scope
// begins.
//
// How: codegen states the same facts the inliner would, so LLVM's alias
// analysis treats the slice as it treats a noalias argument.

package llvm

// noaliasDeclName is the intrinsic NoAliasScopeDecl calls (LLVM 13 and
// later, so within the LLVM 21 floor).
const noaliasDeclName = "llvm.experimental.noalias.scope.decl"

// AliasDomain is a new distinct alias domain !{!self, !"name"}. Distinct,
// so two functions' domains are never one node, as clang's inliner makes
// them.
func (mt *Meta) AliasDomain(name string) MD {
	md := mt.Reserve()
	mt.FillTuple(md, MDRef(md), MDStr(name))
	mt.nodes[md.id-1].text = "distinct " + mt.nodes[md.id-1].text
	return md
}

// AliasScope is a new distinct scope !{!self, !domain, !"name"} of domain.
func (mt *Meta) AliasScope(domain MD, name string) MD {
	mt.check(domain, "AliasScope")
	md := mt.Reserve()
	mt.FillTuple(md, MDRef(md), MDRef(domain), MDStr(name))
	mt.nodes[md.id-1].text = "distinct " + mt.nodes[md.id-1].text
	return md
}

// AliasScope is the attachment !alias.scope !list (a tuple of scopes).
func AliasScope(list MD) Attach { return Attach{"alias.scope", list} }

// NoAlias is the attachment !noalias !list (a tuple of scopes).
func NoAlias(list MD) Attach { return Attach{"noalias", list} }

// NoAliasScopeDecl emits call void
// @llvm.experimental.noalias.scope.decl(metadata !list), declaring the
// intrinsic in the module on first use. list is a tuple of one scope.
func (b *Block) NoAliasScopeDecl(list MD) {
	m := b.f.m
	m.meta.check(list, "NoAliasScopeDecl")
	if !m.noaliasDecl {
		m.claim(noaliasDeclName, "NoAliasScopeDecl")
		m.noaliasDecl = true
	}
	b.begin(nil)
	b.str("call void @" + noaliasDeclName + "(metadata ")
	b.text = appendMDRef(b.text, list)
	b.str(")")
	b.end(nil)
}
