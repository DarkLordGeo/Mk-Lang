// Why: generators (gen functions that run at compile time and emit code)
// need a typed API for reading declarations and building code, and its names
// must be in every module's prelude.
//
// What: the generator API types Gen, Type, Field, Variant, Method, Flow,
// Param, Code and Block, the enums TypeKind, Receiver and FlowKind, and their
// methods (Gen.Emit, Gen.Fresh, Type.Fields, Code.Then and the rest).
//
// How: every type is a plain struct with one private u32 handle, so no module
// outside std can construct one (E0305), and every method is a bodiless
// intrinsic that gen's host implements. The compile-time-only rule (E0705)
// and the splice table are check's and gen's, not this file's.

/// One run of a generator: the receiver of every call that emits code or
/// reports an error.
export struct Gen {
    h: u32
}

/// A type as a generator sees it, with names resolved as in the module of
/// the gen line.
export struct Type {
    h: u32
}

/// A field of a struct, error, record or enum variant.
export struct Field {
    h: u32
}

/// A variant of an enum.
export struct Variant {
    h: u32
}

/// A method of an interface, an inherent function of a named struct or enum
/// (Type.Methods), or a function named as a gen argument (Receiver Static).
export struct Method {
    h: u32
}

/// One flow clause of a method's signature (Method.Flows).
export struct Flow {
    h: u32
}

/// A parameter of an interface method, after the receiver.
export struct Param {
    h: u32
}

/// A run of tokens. A quote { } expression has type Code, and $(...) splices
/// a value into it. A float splices as the shortest decimal that reads back
/// to its bits, and a fn value as its function's qualified name; an operand
/// of a type that cannot splice is E0301 at the splice.
export struct Code {
    h: u32
}

/// A list of statements built one Add at a time; it splices into a quote as
/// its tokens.
export struct Block {
    h: u32
}

/// What kind of type a Type is. Opaque is a name not resolved yet, such as an
/// item that a later gen line makes.
export enum TypeKind {
    /// A named struct.
    Struct
    /// A named enum.
    Enum
    /// An error type.
    Error
    /// An interface.
    Interface
    /// A record type, such as (x: int, y: int).
    Record
    /// A built-in scalar type, such as int or bool.
    Scalar
    /// A reference, &T or mut T.
    Ref
    /// A slice, []T or mut []T.
    Slice
    /// A fixed-length array, [n]T.
    Array
    /// An optional, ?T.
    Optional
    /// A fallible type, E!T.
    Fallible
    /// A function type.
    Fn
    /// A raw pointer, *T.
    RawPtr
    /// A name not resolved yet, such as an item a later gen line makes.
    Opaque
}

/// How a method takes its receiver: none, self, &self or mut self.
export enum Receiver {
    /// No receiver: a function called on the type, or a function named as a gen
    /// argument.
    Static
    /// self: the method takes the receiver by value.
    Value
    /// &self: the method borrows the receiver.
    Ref
    /// mut self: the method borrows the receiver mutably.
    Mut
}

/// What a flow clause states: `from` (the result holds a loan on the
/// parameter), `into` (a parameter flows into another), `ends`, or a store
/// `*q into p`.
export enum FlowKind {
    /// `from`: the result holds a loan on the parameter.
    From
    /// `into`: a parameter flows into another.
    Into
    /// `ends`: the call ends every loan on the named parameter.
    Ends
    /// `*q into p`: what q points at is written into p.
    Store
}

/// Appends the items of c to this run's output; c must parse as top-level
/// items, with no gen or import (E0905). Compile time only.
export Gen.Emit(mut self, c: Code)
/// A fresh empty builder of statements. It holds no loan on the Gen, so Emit
/// may follow while it lives. Compile time only.
export Gen.Block(&self): Block
/// The run's arena, which lasts for the run, for building text and lists with
/// std. What it allocates counts against the run's limits like any other
/// allocation. Compile time only.
export Gen.Arena(&self): &Arena
/// Records E0903 at the gen line with msg. The run continues, and its output is
/// discarded when it ends. Compile time only.
export Gen.Fail(mut self, msg: str)
/// As Fail, but reports E0903 at field f's declaration. Compile time only.
export Gen.FailAt(mut self, f: Field, msg: str)
/// As Fail, but reports E0903 at type t's declaration. Compile time only.
export Gen.FailOn(mut self, t: Type, msg: str)
/// An identifier unique in the module, made from base, a line number and a
/// counter of the run. A base that is not an identifier is E0901.
/// Compile time only.
export Gen.Fresh(mut self, base: str): Code
/// name as one identifier token. A name that is not an identifier, or is a
/// keyword, is E0901. Compile time only.
export Gen.Ident(&self, name: str): Code
/// s as one str literal token, escaped as Mk source writes literals.
/// Compile time only.
export Gen.Str(&self, s: str): Code
/// The bytes of the regular file at path. path is relative to the package
/// root of the gen line's module, written with `/`, without `..`, and
/// reached through no link; the file is at most 64 MiB. Any other path is
/// E0910. The gen line runs again when the file changes. O(n) in the file's
/// bytes. Compile time only.
export Gen.Input(&self, path: str): []u8

/// Appends c, then a newline, to the block. Compile time only.
export Block.Add(mut self, c: Code)
/// A new Code of self's tokens followed by c's; both stay unchanged. Compile
/// time only.
export Code.Then(self, c: Code): Code

/// The type spelled as a resolved reference, generic arguments included
/// (fs.File, List[User]), every name pinned to its declaration. Compile time
/// only.
export Type.Name(&self): Code
/// The bare declared name of a named type (User), else the type's source
/// spelling ([]int). Compile time only.
export Type.Text(&self): str
/// The type's kind; a name not resolved yet is Opaque. Compile time only.
export Type.Kind(&self): TypeKind
/// Whether the named type is exported; true for an unnamed type. Compile time
/// only.
export Type.Exported(&self): bool
/// Type identity. Two Opaque types are the same when they name the same not yet
/// generated item; a resolved and an Opaque type never are. Compile time only.
export Type.Same(&self, other: Type): bool
/// The generic arguments of a named type, in order; const arguments are left
/// out. Compile time only.
export Type.Args(&self): []Type
/// The element type of a reference, slice, array, optional or raw pointer, or
/// the ok type of a fallible; any other kind is E0901. Compile time only.
export Type.Elem(&self): Type
/// The fields of a struct or error in declaration order, or of a record in
/// canonical order, with an instance's arguments substituted. Only exported
/// fields for a type of another module. Opaque is E0906, any other kind E0901.
/// Compile time only.
export Type.Fields(&self): []Field
/// An enum's variants in declaration order, payload types substituted as for
/// Fields; not an enum is E0901. Compile time only.
export Type.Variants(&self): []Variant
/// An interface's methods in declaration order, or the inherent functions of a
/// named struct or enum in declaration order (only exported ones for a type of
/// another module), each a signature read; any other kind is E0901. O(m) in
/// the methods returned. Compile time only.
export Type.Methods(&self): []Method

/// The field's name. Compile time only.
export Field.Name(&self): str
/// The field's type; Opaque when it is not resolved yet. Compile time only.
export Field.Type(&self): Type
/// Whether the field is exported. Compile time only.
export Field.Exported(&self): bool
/// The field's doc comment: the /// lines directly above it, empty when there
/// are none. Compile time only.
export Field.Doc(&self): str

/// The variant's name. Compile time only.
export Variant.Name(&self): str
/// The variant's payload fields, in order. Compile time only.
export Variant.Fields(&self): []Field

/// The method's name. Compile time only.
export Method.Name(&self): str
/// How the method takes its receiver: Static, Value, Ref or Mut. Compile time
/// only.
export Method.Receiver(&self): Receiver
/// The parameters after the receiver, in order. On a generic method it is
/// E0901, so test Generic first. Compile time only.
export Method.Params(&self): []Param
/// The result type with its error set: a Fallible for E!T, the Scalar void
/// when there is no result. On a generic method it is E0901. Compile time
/// only.
export Method.Result(&self): Type
/// Whether the method has type parameters of its own. Compile time only.
export Method.Generic(&self): bool
/// The flow clauses the signature declares, one per source of a `from`, in
/// one fixed order: by kind (From, Into, Ends, Store), then field, then
/// parameter, then path. Inferred sources are not clauses. O(f) in the
/// clauses. Compile time only.
export Method.Flows(&self): []Flow
/// Whether the signature declares from, into, *q into p or ends. O(f) in the
/// clauses. Compile time only.
export Method.HasFlows(&self): bool
/// The signature printed as (receiver, name: type, ...): result, with Self kept
/// and the flows left out. Compile time only.
export Method.Signature(&self): Code

/// The parameter's name. Compile time only.
export Param.Name(&self): str
/// The parameter's type. Compile time only.
export Param.Type(&self): Type
/// Whether the parameter is const. Compile time only.
export Param.Const(&self): bool

/// The clause's kind. O(1). Compile time only.
export Flow.Kind(&self): FlowKind
/// What the clause flows into or ends: `result` for From, the receiving
/// parameter for Into (`x: T into q` gives `q`) and Store (`*q into p` gives
/// `p`), the parameter whose loans end for Ends; the receiver is `self`.
/// O(1). Compile time only.
export Flow.Param(&self): str
/// The source as written: for From the path after `from` (`a`, `self.buf`),
/// for Into the parameter that flows (`x`), for Store the stored parameter
/// (`q`), "" for Ends. O(1). Compile time only.
export Flow.Path(&self): str
/// The result field of a per-field `from` clause, else "". O(1). Compile time
/// only.
export Flow.Field(&self): str
