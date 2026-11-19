// Why: a snapshot can be read offline, to inspect a data directory.
// What: `kvd snap dump|get FILE [KEY]`: RunSnap.
// How: the file is mapped read-only under a shared lock; a snapshot a server
// is writing is locked, and refused rather than waited for.

import src.snap

/// `kvd snap` takes no flags.
const SnapFlags: Accepts = Accepts(values: [], switches: [])

/// `kvd snap`: prints a snapshot's header and entries, or one key's value.
/// Exits 3 on a damaged snapshot and 1 when it is missing or locked.
RunSnap(sys: &System, rest: []str): int *rest into sys {
    args := ParseArgs(sys.arena, rest, &SnapFlags) else e {
        return UsageError(sys, e)
    }
    action := args.Arg(0) else { "" }
    path := args.Arg(1) else {
        sys.PrintErr("kvd: snap needs an action and a file; see kvd --help\n") else {}
        return ExitUsage
    }
    key: ?str = match action {
        "dump" {
            if args.plain.Len() != 2 {
                sys.PrintErr("kvd: snap dump takes one file; see kvd --help\n") else {}
                return ExitUsage
            }
            none
        }
        "get" {
            if args.plain.Len() != 3 {
                sys.PrintErr("kvd: snap get takes a file and a key; see kvd --help\n") else {}
                return ExitUsage
            }
            args.Arg(2)
        }
        _ {
            sys.PrintErr("kvd: unknown snap action {action}; see kvd --help\n", (action)) else {}
            return ExitUsage
        }
    }
    var out := sys.Stdout()
    shown := snap.Show(sys.arena, sys.io, path, key, mut out) else e {
        sys.PrintErr("kvd: {path}: {e}\n", (path, e)) else {}
        return match e {
            snap.NotSnapshot | snap.Damaged { ExitCorrupt }
            OutOfMemory { ExitMemory }
            _ { ExitFailure }
        }
    }
    if !shown {
        sys.PrintErr("kvd: {path}: no snapshot there, or a server is writing it\n", (path)) else {}
        return ExitFailure
    }
    return 0
}
