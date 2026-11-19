// Why: incremental builds reuse a cached result only while everything it
// read is unchanged, so check must record what each item and body reads.
//
// What: recorder, with one method per read kind (signature, shape, fields,
// value, impl, noImpl, noItem, exports, target) and merge.
//
// How: every query that reads another entity records it by stable key and
// kind in a types.ReadSet. Hash is left zero: the driver asks iface for the
// part hashes before storing.

package check

import (
	"mk/internal/types"
)

// recorder adds reads to one item's or one body's read set. A nil recorder
// records nothing (a query made on behalf of no reader, such as a test).
type recorder struct {
	rs *types.ReadSet
}

func (r recorder) add(k types.ReadKind, key types.Key, name string) {
	if r.rs == nil || (key.IsZero() && k != types.ReadTarget) {
		return
	}
	r.rs.Add(types.Read{Kind: k, Key: key, Name: name})
}

// signature records a callee's or member's signature read.
func (r recorder) signature(key types.Key) { r.add(types.ReadSignature, key, "") }

// shape records a type item's shape read.
func (r recorder) shape(key types.Key) { r.add(types.ReadShape, key, "") }

// fields records a type's exported fields read, or all of its fields.
func (r recorder) fields(key types.Key, all bool) {
	name := ""
	if all {
		name = "all"
	}
	r.add(types.ReadFields, key, name)
}

// value records a const's value read.
func (r recorder) value(key types.Key) { r.add(types.ReadValue, key, "") }

// impl records an ImplOf hit: interface iface for the type spelled target.
func (r recorder) impl(iface types.Key, target string) { r.add(types.ReadImpl, iface, target) }

// noImpl records an ImplOf miss (negative).
func (r recorder) noImpl(iface types.Key, target string) { r.add(types.ReadNoImpl, iface, target) }

// noItem records that type or module key has no member or item name.
func (r recorder) noItem(key types.Key, name string) { r.add(types.ReadNoItem, key, name) }

// exports records a member list read (a member lookup that missed).
func (r recorder) exports(key types.Key) { r.add(types.ReadExports, key, "") }

// target records a Target field read: no key, the field's name
// ("abi" is the shape pass's pseudo-field).
func (r recorder) target(field string) { r.add(types.ReadTarget, types.Key{}, field) }

// merge adds an evaluation's reads.
func (r recorder) merge(o *types.ReadSet) {
	if r.rs != nil && o != nil {
		r.rs.Merge(o)
	}
}
