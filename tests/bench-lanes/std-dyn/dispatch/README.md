# dispatch (same algorithm, std-dyn)

The cost of one dynamic call through a dispatch value that holds its function pointer inline beside the object's address, with the target unpredictable. Every column has the same machine-level data structure, C's `{context pointer, function pointer}` pair: C `{void *self; uint64_t (*step)(void *, uint64_t)}`; Zig `{ptr: *anyopaque, step: *const fn}`; Rust `{ctx: &mut Acc, step: fn(&mut Acc, u64) -> u64}` and Go `{ctx *Acc; step func(*Acc, uint64) uint64}`, where both objects are one `Acc` type because safe Rust and Go cannot erase a pointer's type (`unsafe` is banned in this table) and the function pointer is what differs; Mk `gen dyn.Of(Step)`'s `StepDyn`, a `dyn.Ref` (the address and the type's key) and the `fn` held inline, whose adapter compares the key before its direct call. Two differences are the languages' own and are priced, not removed: Mk's key compare (the price of a sound erased handle in user code), and Go's func value, which points to a closure record, so its call loads the code pointer from it (one dependent load more). The languages' native dispatch values (Rust `&mut dyn Trait`, Zig's vtable-pointer pattern, Go's interface) hold a vtable pointer instead, one dependent load more: they are a different data structure and not in this table. `dispatchalt` is the same loop with a predictable target, where the per-call overhead shows. Only `tools/bench` publishes a number from this row.

```
interface Step { Step(mut self, x: u64): u64 }
Add.Step(x):  acc = acc + x;                  return acc      (acc starts at 1)
Xor.Step(x):  acc = (acc ^ x) * 0x100000001b3; return acc     (acc starts at 0xcbf29ce484222325)

objs = [pair(&add, Add.Step), pair(&xor, Xor.Step)]   a two-element array, built once
s = seed; sum = 0
repeat n times:
    s = s + 0x9E3779B97F4A7C15
    z = mix(s)                         splitmix64's output function
    sum = sum + objs[z & 1].Step(z >> 1)
print sum
```

All arithmetic is `u64` and wraps (overflow class `wraps`). `mix(z)`: `z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9; z = (z ^ (z >> 27)) * 0x94D049BB133111EB; return z ^ (z >> 31)`. Each language calls through the pair with no devirtualisation hint; the array index is bounds-checked where the language checks. Sizes: `quick` 1,000,000 calls, publish 500,000,000.
