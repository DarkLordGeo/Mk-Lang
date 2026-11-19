// Why: the simulated clients need a workload drawn from the seed and a model
// their replies are checked against.
// What: Steps, Request, StepValue, KeyName and ApplyStep, and the Rng.
// How: each client works on keys of its own, so its changes reach the store
// in the order it sends them, and the state it was told about is the
// changes it was answered for, applied in order.

import src.store

/// The keys each client uses.
export const KeysPerClient: int = 3

/// SplitMix64 (Steele, Lea and Flood, 2014): one seed gives one sequence on
/// every target.
struct Rng {
    state: u64
}

Rng.Next(mut self): u64 {
    self.state = self.state +% 0x9E3779B97F4A7C15
    var z := self.state
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

/// A number from 0 to n - 1.
Rng.Below(mut self, n: int): int {
    return Cast[int](self.Next() % Cast[u64](n))
}

/// What a client does at one step.
export enum Kind {
    Set
    Append
    Incr
    Del
    GetSet
    Get
}

/// One step of a client's workload.
export struct Step {
    export kind: Kind
    export key: int
}

/// The steps of client `client` in the run of `seed`, written into `out`.
export Steps(seed: u64, client: int, out: mut []Step) {
    var r := Rng(state: seed *% 0x2545F4914F6CDD1D +% Cast[u64](client) *% 0x9E3779B97F4A7C15)
    for s in mut out {
        roll := r.Below(100)
        kind := if roll < 35 {
            Kind.Set
        } else if roll < 55 {
            Kind.Append
        } else if roll < 72 {
            Kind.Incr
        } else if roll < 82 {
            Kind.Del
        } else if roll < 92 {
            Kind.GetSet
        } else {
            Kind.Get
        }
        *s = Step(kind: kind, key: r.Below(KeysPerClient))
    }
}

/// The name of a client's key.
export KeyName(a: &Arena, client: int, key: int): OutOfMemory!str from a {
    return try a.Fmt("c{client}k{key}", (client, key))
}

/// The value step `step` writes, for the steps that write one.
export StepValue(a: &Arena, seed: u64, client: int, step: int, kind: Kind): OutOfMemory!str from a {
    return match kind {
        .Set { try a.Fmt("v{seed}-{client}-{step}", (seed, client, step)) }
        .Append { try a.Fmt("+{step}", (step)) }
        .GetSet { try a.Fmt("g{step}", (step)) }
        _ { "" }
    }
}

/// The request line of a step.
export Request(a: &Arena, seed: u64, client: int, step: int, s: &Step): OutOfMemory!str from a {
    key := try KeyName(a, client, s.key)
    value := try StepValue(a, seed, client, step, s.kind)
    return match s.kind {
        .Set { try a.Fmt("SET {key} {value}", (key, value)) }
        .Append { try a.Fmt("APPEND {key} {value}", (key, value)) }
        .Incr { try a.Fmt("INCR {key}", (key)) }
        .Del { try a.Fmt("DEL {key}", (key)) }
        .GetSet { try a.Fmt("GETSET {key} {value}", (key, value)) }
        .Get { try a.Fmt("GET {key}", (key)) }
    }
}

/// Applies a step to a model store as the server applies it: an INCR of a
/// value that is not an integer changes nothing.
export ApplyStep(a: &Alloc into m, arena: &Arena, m: mut store.Store, seed: u64, client: int, step: int, s: &Step): OutOfMemory!void {
    key := try KeyName(arena, client, s.key)
    value := try StepValue(arena, seed, client, step, s.kind)
    match s.kind {
        .Set | .GetSet {
            try m.Set(a, key, value)
        }
        .Append {
            _ = try m.Append(a, key, value)
        }
        .Incr {
            text := m.Get(key) else { "0" }
            n := text.ParseInt() else {
                return
            }
            try m.Set(a, key, try arena.Fmt("{v}", (v: n + 1)))
        }
        .Del {
            _ = m.Del(a, key)
        }
        .Get {}
    }
}
