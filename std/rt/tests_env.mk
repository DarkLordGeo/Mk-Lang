// Why: rt.Env must answer from the environment rt decoded at start, even
// after std changes the live OS block.
// What: tests of rt.Env (std/rt/env.mk) against the live block, System.Env
// and the snapshot after sys.Inherited.
// How: a block whose name carries "[env ...]" is run again alone with that
// environment by internal/driver's TestStdRuntimeTests; each block also
// holds without it, since it checks against the live OS block or System.Env
// rather than fixed values alone.

import std.sys

// EnvSp is the stack pointer at entry, from which std.sys reads the live
// environment on Linux (unused on Windows).
EnvSp(): *u8 {
    unsafe "the blocks run after Start, so R() is the page Init mapped, which lives until exit" {
        return (*R()).init_sp
    }
}

// EnvDropInherit is sys.Inherited, for RT-Env-AfterLive: in a test block
// with no parameters, sys names the block's System when the registry's Go
// test builds the module (std/rt_term_test.go), so the module is reached
// from here.
EnvDropInherit() {
    _ = sys.Inherited(EnvSp())
}

// EnvRawInherit is the live OS block's length for MK_INHERIT
// (sys.GetEnvRaw), -1 when it is unset; as EnvDropInherit, outside a test
// block.
EnvRawInherit(): int {
    var buf: [32]u8 = [0; 32]
    return sys.GetEnvRaw(EnvSp(), "MK_INHERIT", mut buf[..])
}

// EnvLive is whether rt.Env(name) holds what the live OS block holds for
// name now: both unset, or the same bytes. GetEnvRaw is the OS's own
// lookup, so on Windows it folds case as GetEnvironmentVariableW does.
EnvLive(name: str): bool {
    var buf: [256]u8 = [0; 256]
    n := sys.GetEnvRaw(EnvSp(), name, mut buf[..])
    got := Env(name)
    if n < 0 {
        return got.IsNone()
    }
    v := got else {
        return false
    }
    if v.Len() != n {
        return false
    }
    // A longer value (PATH) is compared on its length and first 256 bytes.
    b := v.AsBytes()
    m := if n < 256 { n } else { 256 }
    for i in 0..m {
        if b[i] != buf[i] {
            return false
        }
    }
    return true
}

// EnvSame is whether two lookups gave the same answer.
EnvSame(a: ?str, b: ?str): bool {
    x := a else {
        return b.IsNone()
    }
    y := b else {
        return false
    }
    return x == y
}

// EnvSwap writes name with its ASCII letters' case swapped into buf and
// returns the length, or -1 when it does not fit.
EnvSwap(name: str, buf: mut []u8): int {
    b := name.AsBytes()
    if b.Len() > buf.Len() {
        return -1
    }
    for i in 0..b.Len() {
        var c := b[i]
        if c >= b'a' && c <= b'z' {
            c -= 32
        } else if c >= b'A' && c <= b'Z' {
            c += 32
        }
        buf[i] = c
    }
    return b.Len()
}

test "RT-Env-Snapshot [env MK_ENV_ONE=one MK_ENV_EMPTY=] rt.Env answers each variable as the OS block holds it, an empty value as some empty text, and folds ASCII case only on Windows" {
    for name in ["MK_ENV_ONE", "MK_ENV_EMPTY", "mk_env_one", "Mk_Env_Empty", "MK_ENV_NEVER_SET_725", "PATH", "Path", "path"] {
        Assert(EnvLive(name))
    }
    Assert(Env("MK_ENV_NEVER_SET_725").IsNone())
    Assert(Env("MK_ENV_ONE=one").IsNone())
    // With the block's environment (TestStdRuntimeTests' second run), the
    // values themselves.
    if one := Env("MK_ENV_ONE") {
        Assert(one == "one")
        if Target.os == Os.Windows {
            Assert(EnvSame(Env("MK_ENV_EMPTY"), some("")))
            Assert(EnvSame(Env("mk_env_one"), some("one")))
            Assert(EnvSame(Env("Mk_Env_Empty"), some("")))
        } else {
            // A WSL child gets the block's environment through WSLENV; the
            // empty value is checked against the live block above, and here
            // only when it arrived.
            if e := Env("MK_ENV_EMPTY") {
                Assert(e == "")
            }
            Assert(Env("mk_env_one").IsNone())
            Assert(Env("Mk_Env_Empty").IsNone())
        }
    }
}

test "RT-Env-System rt.Env answers as System.Env for every variable of the snapshot, and for its name with ASCII case swapped" (sys: &System) {
    var buf: [256]u8 = [0; 256]
    var seen := 0
    unsafe "the blocks run after Start, so Proc() is the page Init mapped, which lives until exit" {
        for v in (*Proc()).env {
            Assert(EnvSame(Env(v.name), sys.Env(v.name)))
            n := EnvSwap(v.name, mut buf[..])
            if n >= 0 {
                // Swapping ASCII letters keeps the bytes valid UTF-8.
                swapped := StrFrom(buf[0..n]).Must()
                Assert(EnvSame(Env(swapped), sys.Env(swapped)))
            }
            seen += 1
        }
    }
    // The test runner's environment is not empty on either target.
    Assert(seen > 0)
}

test "RT-Env-AfterLive [env MK_INHERIT=4000000] rt.Env answers from the snapshot after sys.Inherited removes MK_INHERIT from the live Windows block" {
    before := Env("MK_INHERIT")
    // 4000000 names no socket, so Inherited answers Other; on Windows it
    // has removed MK_INHERIT from the live block first.
    EnvDropInherit()
    Assert(EnvSame(Env("MK_INHERIT"), before))
    if v := before {
        Assert(v == "4000000")
        live := EnvRawInherit()
        if Target.os == Os.Windows {
            Assert(live == -1)
        } else {
            Assert(live == 7)
        }
    }
}
