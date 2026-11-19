// Why: data that arrives in pieces (a stream, a file read in blocks) needs
// XXH64 without first joining the pieces.
//
// What: Xx64State, XXH64 over bytes that arrive in pieces, as xxhash.h's
// XXH64_update and XXH64_digest compute it.
//
// How: four lane accumulators absorb whole 32-byte stripes straight from the
// input; a partial stripe waits in the state. Sum runs Xx64's own merge and
// tail (xx.mk) over the state, so Bytes in any split, then Sum, equals Xx64
// of the concatenation. No allocation; the state is plain.

/// Xx64State is an XXH64 hash in progress under a seed. It is not
/// zero-valid: a zero-filled state does not hold the seed's starting
/// values. Make one with New.
export struct Xx64State {
    v1: u64
    v2: u64
    v3: u64
    v4: u64
    seed: u64
    total: u64
    tail: [32]u8
    tn: int
    invariant tn >= 0 && tn < 32
}

/// Returns the state of the empty message under seed. O(1); allocates
/// nothing.
export Xx64State.New(seed: u64): Xx64State {
    return Xx64State(v1: seed +% XxPrime1 +% XxPrime2, v2: seed +% XxPrime2, v3: seed, v4: seed -% XxPrime1, seed: seed, total: 0, tail: [0; 32], tn: 0)
}

/// Absorbs data. Bytes in any split gives the same value as Xx64 of the
/// concatenation. Whole 32-byte stripes are read from data in place.
/// O(data.Len()); allocates nothing, and copies at most 31 bytes.
export Xx64State.Bytes(mut self, data: []u8) {
    n := data.Len()
    self.total = self.total +% Trunc[u64](n)
    var i := 0
    if self.tn > 0 {
        tn := self.tn
        k := if 32 - tn > n { n } else { 32 - tn }
        (mut self.tail[tn..tn + k]).CopyFrom(data[0..k])
        i = k
        if tn + k < 32 {
            self.tn = tn + k
            return
        }
        t := self.tail
        self.Stripe(t[..], 0)
        self.tn = 0
    }
    for n - i >= 32 {
        self.Stripe(data, i)
        i += 32
    }
    if i < n {
        (mut self.tail[0..n - i]).CopyFrom(data[i..n])
        self.tn = n - i
    }
}

/// Returns XXH64 of everything absorbed under the state's seed; the state
/// can keep absorbing. O(1): at most 31 bytes of tail. It allocates
/// nothing.
export Xx64State.Sum(self): u64 {
    acc := if self.total >= 32 {
        Xx64Merge(self.v1, self.v2, self.v3, self.v4)
    } else {
        self.seed +% XxPrime5
    }
    return Xx64Finish(acc +% self.total, self.tail[0..self.tn])
}

// Xx64State.Stripe absorbs the 32 bytes of b at i into the four lanes.
Xx64State.Stripe(mut self, b: []u8, i: int) {
    self.v1 = Xx64Round(self.v1, Le64(b, i))
    self.v2 = Xx64Round(self.v2, Le64(b, i + 8))
    self.v3 = Xx64Round(self.v3, Le64(b, i + 16))
    self.v4 = Xx64Round(self.v4, Le64(b, i + 24))
}
