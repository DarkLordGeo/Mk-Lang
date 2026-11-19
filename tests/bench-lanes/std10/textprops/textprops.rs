// textprops, Rust, idiomatic std: char::is_alphabetic (variant
// alphabetic: the Alphabetic property, core's generated tables) over
// 65,536 scalars drawn by splitmix64 from six stable ranges (README.md),
// reps passes; the checksum counts the hits plus the pass's index times
// each hit. Variant upper is skipped (bench.json): char::is_uppercase is
// the Uppercase property, not General_Category Lu, and std has no
// category.
// Usage: textprops <variant> <reps> <seed>.
const N: usize = 65536;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

const LO: [u32; 6] = [0x20, 0xC0, 0x391, 0x410, 0x3041, 0x4E00];
const LEN: [u32; 6] = [95, 400, 57, 64, 86, 20902];

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 || args[1] != "alphabetic" {
        eprintln!("usage: textprops alphabetic <reps> <seed>");
        std::process::exit(2);
    }
    let reps: u64 = args[2].parse().unwrap();
    let mut st: u64 = args[3].parse().unwrap();
    let mut cs = Vec::with_capacity(N);
    for _ in 0..N {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let z = mix(st);
        let k = (z % 6) as usize;
        cs.push(char::from_u32(LO[k] + ((z >> 8) % LEN[k] as u64) as u32).unwrap());
    }
    let mut acc: u64 = 0;
    for r in 0..reps {
        for &c in &cs {
            if c.is_alphabetic() {
                acc = acc.wrapping_add(1 + r);
            }
        }
    }
    println!("{}", acc);
}
