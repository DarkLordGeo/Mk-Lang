// Why: a program sometimes needs a data file or the build profile as a
// compile-time constant, and the incremental build must know what each
// module read.
//
// What: std.build: File (a file's bytes as static data), Profile and
// ProfileKind (Dev, Release, Test, Checked).
//
// How: both are compile-time facts the driver supplies through the
// evaluator (internal/eval intrinsic_build.go); every file read and the
// profile are inputs of the reading module's key.

/// The profile a build runs in. `mk build`, `mk run` and `mk check` use Dev,
/// `--release` and `--profile size` use Release, `mk test` uses Test, and
/// `--profile checked` uses Checked: compiled and linked as Release is, with
/// contracts checked as in Dev.
export enum ProfileKind {
    /// The profile of mk build, mk run and mk check.
    Dev
    /// The profile of --release and --profile size.
    Release
    /// The profile of mk test.
    Test
    /// The profile of --profile checked: compiled and linked as Release, with
    /// contracts checked as in Dev.
    Checked
}

/// The current build's profile, a compile-time constant. A module that reads it
/// is compiled separately for each profile, and may differ between them.
export const Profile: ProfileKind = ProfileOf()

/// The bytes of the regular file at path, embedded in the program as static
/// data. path is relative to the root of the package that holds the calling
/// module, written with `/`, with no `..`, no absolute path and no drive
/// letter. The file must not be reached through a link and may hold at most 64
/// MiB; anything else is E0910. Compile time only: a call outside a const or
/// global initializer or a generator is E0705. Editing the file rebuilds
/// exactly the modules that read it.
export File(path: str): []u8

// ProfileOf is the driver's profile, which the evaluator answers.
ProfileOf(): ProfileKind
