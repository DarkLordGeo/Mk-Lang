// Why: every failure must reach the user as a sentence and a fixed exit
// status, whichever module it came from.
//
// What: Problem, the union of every error a command can meet, and Ctx.Say,
// Ctx.Usage, Ctx.Fail, Ctx.Files and Ctx.Bad.
//
// How: one match over Problem picks the message and the status: 1 for a bad
// archive, 3 for the file system, 4 for memory.

import src.archive
import src.keys
import src.seal
import src.tar
import src.tree
import std.compress
import std.fs

/// Every error a command reports.
type Problem = tree.Error | keys.Error | archive.Error | seal.Error | tar.Error | compress.Error | ReadError | WriteError

/// Writes the message to stderr. A failed write to stderr has nowhere left
/// to be reported, so it is dropped.
Ctx.Say[A: Fields[Format] + Plain](&self, const f: str, args: A) {
    self.sys.PrintErr(f, args) else {}
}

/// Reports a usage error.
Ctx.Usage(&self, command: str, message: str): Failed {
    self.Say("pack {command}: {message}\n", (command, message))
    return Failed(code: ExitUsage)
}

/// Reports `e` about `subject`, a path, and gives the error that exits with
/// its status: 1 for an archive that is damaged, forged or does not open,
/// 3 for the file system, 4 for memory.
Ctx.Fail(&self, subject: str, e: Problem): Failed {
    // one arm a member: an `other` arm would bind the whole set
    code := match e {
        OutOfMemory {
            if self.limit > 0 {
                self.Say("pack: memory budget of {n} bytes exhausted\n", (n: self.limit))
            } else {
                self.Say("pack: out of memory\n")
            }
            ExitMemory
        }
        fs.NotFound { self.Files(subject, "no such file or directory") }
        fs.Exists { self.Files(subject, "already exists") }
        fs.Denied { self.Files(subject, "permission denied") }
        fs.IsDirectory { self.Files(subject, "is a directory") }
        fs.NotDirectory { self.Files(subject, "not a directory") }
        fs.NoSpace { self.Files(subject, "no space left on the device") }
        fs.InvalidPath { self.Files(subject, "not a valid path") }
        fs.Unsupported { self.Files(subject, "not supported by this system") }
        Utf8Error { self.Files(subject, "not valid UTF-8") }
        fs.Os(code) {
            self.Say("pack: {subject}: the system refused (error {code})\n", (subject, code))
            ExitFiles
        }
        Canceled { self.Files(subject, "canceled") }
        ReadError { self.Files(subject, "cannot read") }
        WriteError { self.Files(subject, "cannot write") }
        tree.BadName { self.Files(subject, "holds a name that is not UTF-8, or has a newline, \\ or :") }
        tree.Changed { self.Files(subject, "a file changed while it was read") }
        keys.BadKeyFile(line) {
            self.Say("pack: {subject}:{line}: not a pack key file\n", (subject, line))
            ExitBad
        }
        archive.NotArchive { self.Bad(subject, "not a pack archive") }
        archive.Unsupported(version) {
            self.Say("pack: {subject}: archive version {version} is not supported\n", (subject, version))
            ExitBad
        }
        archive.Cut(section) {
            self.Say("pack: {subject}: the archive ends inside its {section}\n", (subject, section))
            ExitBad
        }
        archive.BadHeader { self.Bad(subject, "the archive header is damaged") }
        archive.BadManifest(line) {
            self.Say("pack: {subject}: manifest line {line} is damaged\n", (subject, line))
            ExitBad
        }
        archive.UnsafePath(line) {
            self.Say("pack: {subject}: manifest line {line} names a path outside the destination\n", (subject, line))
            ExitBad
        }
        archive.BadSignature { self.Bad(subject, "the signature does not match the manifest") }
        archive.Unsigned { self.Bad(subject, "the archive is not signed") }
        archive.OtherSigner { self.Bad(subject, "the archive is signed by another key") }
        archive.DataDigest { self.Bad(subject, "the data does not match the manifest's digest") }
        archive.DataSize { self.Bad(subject, "the data is not the size the manifest says") }
        archive.EntryDigest(index) {
            self.Say("pack: {subject}: entry {index} does not match its digest\n", (subject, index))
            ExitBad
        }
        seal.NotSealed { self.Bad(subject, "not a sealed archive") }
        seal.WrongKey { self.Bad(subject, "wrong key, or the sealed header is damaged") }
        seal.Cut { self.Bad(subject, "the sealed archive is cut short, or has bytes after its end") }
        seal.BadChunk(index) {
            self.Say("pack: {subject}: sealed chunk {index} is damaged\n", (subject, index))
            ExitBad
        }
        seal.WeakKey { self.Bad(subject, "the key is a weak X25519 point") }
        seal.Version(version, cipher) {
            self.Say("pack: {subject}: sealed archive version {version}, cipher {cipher}, is not supported\n", (subject, version, cipher))
            ExitBad
        }
        tar.BadHeader(offset) {
            self.Say("pack: {subject}: the tar header at byte {offset} is damaged\n", (subject, offset))
            ExitBad
        }
        tar.Cut(offset) {
            self.Say("pack: {subject}: the tar archive ends early, at byte {offset}\n", (subject, offset))
            ExitBad
        }
        tar.BadName(offset) {
            self.Say("pack: {subject}: the tar header at byte {offset} names a path outside the destination\n", (subject, offset))
            ExitBad
        }
        compress.Corrupt(offset) {
            self.Say("pack: {subject}: the compressed data is corrupt at byte {offset}\n", (subject, offset))
            ExitBad
        }
        compress.Truncated { self.Bad(subject, "the compressed data ends early") }
        compress.TooLarge { self.Bad(subject, "the data is larger than the manifest says") }
        _ {
            Panic("compress.Error gained a member this match does not handle")
        }
    }
    return Failed(code: code)
}

/// Reports a file system problem and gives its status.
Ctx.Files(&self, subject: str, meaning: str): int {
    self.Say("pack: {subject}: {meaning}\n", (subject, meaning))
    return ExitFiles
}

/// Reports a damaged or forged archive and gives its status.
Ctx.Bad(&self, subject: str, meaning: str): int {
    self.Say("pack: {subject}: {meaning}\n", (subject, meaning))
    return ExitBad
}
