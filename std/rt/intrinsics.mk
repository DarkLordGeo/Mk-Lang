// Why: some runtime operations (calling Main, reading the stack pointer, the
// tables of functions and profile counters) only codegen can produce, yet
// std.rt code must call them like functions.
//
// What: the runtime-only intrinsics and their types: Site, MainResult,
// ProfileRecord, FuncRow, CallMain, ErrorName, ProfileTable, FuncTable,
// StackPointer and UsesTasks.
//
// How: the declarations are bodiless: codegen implements each by its stable
// key, and compile-time evaluation refuses them. CallMain and StackPointer
// are exported for std.task.

/// A source position, as every trap block passes it: file is
/// package-relative with /.
export struct Site {
    /// The file, relative to its package, with / separators.
    export file: str
    /// The line.
    export line: u32
    /// The column.
    export col: u32
}

/// What CallMain returns: how Main ended.
export struct MainResult {
    /// 0 when Main returned normally, else the code of the error it failed with.
    export code: u32
    /// Main's int result when code is 0 (0 for a void Main).
    export exit: int
}

/// One function's counters in a --pgo-gen build (profile.mk writes them).
export struct ProfileRecord {
    /// The function's stable key.
    export key: str
    /// The checksum of the function the counters were taken against.
    export sum: [16]u8
    /// One counter per instrumented edge.
    export counts: []u64
}

/// kind 0 a Mk function (site: its name's position), 1 __chkstk or
/// mk_chkstk_65536, 2 another support routine, thunk or adapter, 3 a
/// memory function C code calls too; sites are null but kind 0's.
export struct FuncRow {
    /// The function's entry address.
    export addr: *u8
    /// Where it is declared, for kind 0; null otherwise.
    export site: *Site
    /// 0 to 3, as above.
    export kind: u32
}

/// CallMain calls the root's Main in whichever of its four forms.
/// Its cost and allocation are Main's.
export CallMain(sys: &System): MainResult

// ErrorName is the display name of an error code.
ErrorName(code: u32): str

// ProfileTable is the PGO table of a --pgo-gen build, else empty.
ProfileTable(): []ProfileRecord

// FuncTable has one row per emitted function and support routine.
FuncTable(): []FuncRow

/// StackPointer reads rsp (sp on aarch64) by inline asm.
/// O(1): one instruction; allocates nothing.
export StackPointer(): uint

/// UsesTasks is whether the program can use tasks: a
/// constant of the build, false when no instance reached from Main, a test
/// or an export spawns, enters a scope, waits on a word or reads an IoTable
/// entry that needs the poller or another task. Start then runs Main on
/// carrier 0's system stack with no scheduler, pool or poller. mono folds
/// every call; a call it did not fold answers true.
/// O(1); allocates nothing.
export UsesTasks(): bool
