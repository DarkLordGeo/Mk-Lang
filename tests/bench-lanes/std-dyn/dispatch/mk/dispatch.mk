// dispatch, Mk, same algorithm: ../README.md's loop of
// n dynamic calls through a two-element array of StepDyn, made by gen
// dyn.Of(Step) over an Add and a Xor, the target picked by a splitmix64
// bit. StepDyn holds the object's erased reference and its fn table inline:
// each call loads the fn from the StepDyn, calls the adapter indirectly,
// and the adapter compares the type key before its direct call of Step.
// u64 with the wrapping operators. Usage: dispatch <n> <seed>. Prints the
// checksum.
import std.dyn

interface Step {
    Step(mut self, x: u64): u64
}

gen dyn.Of(Step)

struct Add {
    acc: u64
}

impl Step for Add {
    Step(mut self, x: u64): u64 {
        self.acc = self.acc +% x
        return self.acc
    }
}

struct Xor {
    acc: u64
}

impl Step for Xor {
    Step(mut self, x: u64): u64 {
        self.acc = (self.acc ^ x) *% 0x100000001b3
        return self.acc
    }
}

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Run(n: int, seed: u64): u64 {
    var a := Add(acc: 1)
    var x := Xor(acc: 0xcbf29ce484222325)
    var objs := [StepDyn.Of(mut a), StepDyn.Of(mut x)]
    var st := seed
    var sum: u64 = 0
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        z := Mix(st)
        sum = sum +% objs[Cast[int](z & 1)].Step(z >> 1)
    }
    return sum
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    sum := Run(n, seed)
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
