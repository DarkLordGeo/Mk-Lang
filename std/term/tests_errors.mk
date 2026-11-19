// Why: every std.term failure goes through Check and OpenCheck, so their
// mapping must hold for each runtime answer.
// What: test blocks for errors.mk's map from core.IoResult to term.Error.
// How: feeds hand-built IoResults to Check and OpenCheck and matches the
// error member that comes back.

import std.core

// OpenName is the member of term.Error OpenCheck gives for r ("ok" none).
OpenName(r: core.IoResult): str {
    _ = OpenCheck(r) else e {
        return match e {
            OutOfMemory { "OutOfMemory" }
            NotTerminal { "NotTerminal" }
            Device(code) { if code == 0 { "Device0" } else { "Device" } }
            _ { "other" }
        }
    }
    return "ok"
}

// CheckName is OpenName for Check.
CheckName(r: core.IoResult): str {
    _ = Check(r) else e {
        return match e {
            OutOfMemory { "OutOfMemory" }
            NotTerminal { "NotTerminal" }
            Device(code) { if code == 0 { "Device0" } else { "Device" } }
            _ { "other" }
        }
    }
    return "ok"
}

test "Open's runtime answer of Other with no OS code (a refused handle record) is OutOfMemory, and Check keeps Device for TermMode's short buffer" {
    refused := core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
    Assert(OpenName(refused) == "OutOfMemory")
    Assert(CheckName(refused) == "Device0")
    // An OS code is the terminal's refusal, and no terminal stays
    // NotTerminal.
    Assert(OpenName(core.IoResult(value: 0, err: core.SysErr.Other, os: 13)) == "Device")
    Assert(OpenName(core.IoResult(value: 0, err: core.SysErr.NotFound, os: 6)) == "NotTerminal")
    Assert(OpenName(core.IoResult(value: 3, err: core.SysErr.None, os: 0)) == "ok")
}
