// Why: an unknown encoding label needs a clear message.
// What: LookupLabel and Problem.
// How: wraps encoding.Lookup and names what is wrong with the label.

import std.encoding

/// What a failed `encoding.Lookup` says about its label.
export Problem(e: &(encoding.UnknownLabel | encoding.Unsupported)): str {
    return match *e {
        encoding.UnknownLabel { "not a label of the Encoding Standard" }
        encoding.Unsupported { "a multi-byte CJK encoding, which textkit does not convert" }
    }
}

/// The encoding `label` names, or `none` after reporting why it names none
/// on `s.err`, as `textkit <command>: <flag> <label>: <problem>`. O(log n)
/// in the label table; allocates nothing.
export LookupLabel(s: mut Streams, command: str, flag: str, label: str): ?encoding.Encoding {
    found := encoding.Lookup(label) else e {
        Print(mut s.err, "textkit {command}: {flag} {label}: {p}\n", (command, flag, label, p: Problem(&e))) else {}
        return none
    }
    return some(found)
}
