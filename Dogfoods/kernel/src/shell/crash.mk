// Why: the panic hook's report must be shown for each kind of failure.
//
// What: `crash KIND` and the list of Kinds.
//
// How: panics on purpose; every operand comes from the command line, so
// nothing can be decided at compile time.

import src.arch
import src.console

const Kinds: str = "must, bounds, overflow, divide, opcode, page"

Crash(out: mut console.Console, words: &HeapList[str]): bool {
    if words.Len() != 2 {
        out.Say("error: crash: one kind is needed: {kinds}\n", (kinds: Kinds))
        return false
    }
    kind := words[1]
    n := words.Len()
    match kind {
        "must" {
            found := kind.Find("#")
            out.Say("crash: {found}\n", (found: found.Must()))
        }
        "bounds" {
            out.Say("crash: {word}\n", (word: words[n + 3]))
        }
        "overflow" {
            big: i64 = 0x7FFFFFFFFFFFFFF0
            out.Say("crash: {sum}\n", (sum: big + Cast[i64](n) * 8))
        }
        "divide" {
            out.Say("crash: {q}\n", (q: 100 / (n - 2)))
        }
        "opcode" {
            arch.InvalidOpcode()
        }
        "page" {
            out.Say("crash: {v}\n", (v: arch.ReadUnmapped()))
        }
        _ {
            out.Say("error: crash: unknown kind '{kind}'; the kinds are {kinds}\n", (kind, kinds: Kinds))
            return false
        }
    }
    out.Say("error: crash: {kind} did not panic\n", (kind))
    return false
}
