// fannkuch, Rust: the reference algorithm of README.md (the Benchmarks
// Game's fannkuch-redux, sequential). Three Vec<i64> of n indexed perm[i]
// (bounds checked; an i64 index is converted with `as usize`), the same loops
// and next-permutation step as every other column. + and - wrap in "rust"
// and trap in "rust-ovf". No unsafe, no slice::reverse, no crates.

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: fannkuch <n>");
        std::process::exit(2);
    }
    let n: i64 = args[1].parse().unwrap();
    let nu = n as usize;
    let mut perm: Vec<i64> = vec![0; nu];
    let mut perm1: Vec<i64> = vec![0; nu];
    let mut count: Vec<i64> = vec![0; nu];
    for i in 0..nu {
        perm1[i] = i as i64;
    }
    let mut r: i64 = n;
    let mut perm_count: i64 = 0;
    let mut checksum: i64 = 0;
    let mut max_flips: i64 = 0;
    let mut done = false;
    while !done {
        while r != 1 {
            count[(r - 1) as usize] = r;
            r -= 1;
        }
        for i in 0..nu {
            perm[i] = perm1[i];
        }
        let mut flips: i64 = 0;
        let mut k = perm[0];
        while k != 0 {
            let mut i: i64 = 0;
            let mut j: i64 = k;
            while i < j {
                let t = perm[i as usize];
                perm[i as usize] = perm[j as usize];
                perm[j as usize] = t;
                i += 1;
                j -= 1;
            }
            flips += 1;
            k = perm[0];
        }
        if flips > max_flips {
            max_flips = flips;
        }
        if perm_count % 2 == 0 {
            checksum += flips;
        } else {
            checksum -= flips;
        }
        loop {
            if r == n {
                done = true;
                break;
            }
            let perm0 = perm1[0];
            for i in 0..r as usize {
                perm1[i] = perm1[i + 1];
            }
            perm1[r as usize] = perm0;
            count[r as usize] -= 1;
            if count[r as usize] > 0 {
                break;
            }
            r += 1;
        }
        perm_count += 1;
    }
    println!("{}", checksum * 1000 + max_flips);
}
