// dispatch, Rust, same algorithm: ../README.md's loop of n dynamic calls
// through a two-element array of {context reference, fn pointer} pairs, the
// layout of the C column (the fn pointer inline, no vtable), over an Add
// and a Xor, the target picked by a splitmix64 bit: one load of the fn
// pointer, one indirect call. Safe Rust cannot erase the context's type
// without unsafe (banned in this table), so both objects are one Acc type
// and the fn pointer is what differs, which leaves the machine-level data
// structure C's. u64 with wrapping_*. Usage: dispatch <n> <seed>. Prints the
// checksum.
struct Acc {
    acc: u64,
}

struct Step<'a> {
    ctx: &'a mut Acc,
    step: fn(&mut Acc, u64) -> u64,
}

fn add_step(a: &mut Acc, x: u64) -> u64 {
    a.acc = a.acc.wrapping_add(x);
    a.acc
}

fn xor_step(o: &mut Acc, x: u64) -> u64 {
    o.acc = (o.acc ^ x).wrapping_mul(0x100000001b3);
    o.acc
}

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn run(n: u64, seed: u64) -> u64 {
    let mut a = Acc { acc: 1 };
    let mut x = Acc { acc: 0xcbf29ce484222325 };
    let mut objs = [Step { ctx: &mut a, step: add_step }, Step { ctx: &mut x, step: xor_step }];
    let mut st = seed;
    let mut sum: u64 = 0;
    for _ in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let z = mix(st);
        let s = &mut objs[(z & 1) as usize];
        sum = sum.wrapping_add((s.step)(s.ctx, z >> 1));
    }
    sum
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: u64 = args[1].parse().unwrap();
    let seed: u64 = args[2].parse().unwrap();
    println!("{}", run(n, seed));
}
