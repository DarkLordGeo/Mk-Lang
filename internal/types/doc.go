// Why: every pass after resolution reads the same semantic data (types,
// items, signatures, facts, const values), so its shape lives in one package
// that holds data and no logic.
//
// What: the package doc of types, listing its parts.
//
// How: IDs follow insertion order and are never in output; keys and names
// are. The binary encodings and hashes are internal/iface's.

// Package types holds the semantic contract every pass after resolution
// reads:
//
//   - Table: hash-consed TypeIDs (equality is ==) with the primitives at fixed
//     IDs, error sets sorted by stable key, and TypeString for diagnostics.
//   - Items, Item, Module, Key: entities with stable keys (ItemKey, MemberKey,
//     ImplKey, TestKey, GenKey, LiteralKey), ErrorCode (an error's run-time
//     identity), the checked declarations (StructDecl .. GenDecl) and
//     KnownItems.
//   - Signature, Param, Flows, Source, Store, Contract, GenericParam, Bound:
//     signatures with flows as data; ParamIndex is 1-based, equal to the
//     parameter's MIR local.
//   - Kind, Shape (with Freeze), Facts, Layout, ShapeTerm, AbiClass: what the
//     shape pass computes for codegen, borrow, abi and the interface.
//   - Target, Arch, OS, Profile, TargetField.
//   - ConstValue, Reloc, ConstTable: canonical compile-time values.
//   - Read, ReadSet: read-set records with negative reads.
//   - Evaluator, EvalRequest, EvalResult, EvalError, Stage, Refusal, PanicCode:
//     the evaluator interface, the one place the stage order is visible.
//
// IDs (TypeID, ItemID, ModuleID, ConstID, ErrorSetID) follow insertion order:
// output and hashes use Keys and names, never IDs. The stable binary encodings
// and hashes of types, signatures and consts are internal/iface's; the
// interning keys here are in-memory only.
package types
