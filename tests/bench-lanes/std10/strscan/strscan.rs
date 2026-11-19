// strscan, Rust, idiomatic std: <[u8]>::is_ascii (variant isascii; core's
// word-at-a-time scan), iter().rposition (variant lastindexbyte; std's
// slice search from the end) and str::encode_utf16().count() (variant
// utf16len) over the text of README.md, reps calls, call r on the text from
// offset r mod 64. The checksum adds each answer (a bool as 0 or 1) and the
// offset. Rust's std has no public WTF-8 validator (skipped, bench.json).
// Usage: strscan <variant> <size> <reps> <seed>.
fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

fn fill(buf: &mut [u8], ascii: bool, mut st: u64) {
    let n = buf.len();
    let mut i = 0;
    while i < n && i < 128 {
        buf[i] = b'a' + (i % 26) as u8;
        i += 1;
    }
    while i < n {
        st = st.wrapping_add(0x9E3779B97F4A7C15);
        let z = mix(st);
        let p = if ascii { 0 } else { z % 8 };
        if p < 5 || i + 4 > n {
            buf[i] = b'a' + ((z >> 8) % 26) as u8;
            i += 1;
        } else if p == 5 {
            buf[i..i + 2].copy_from_slice(&[0xC3, 0xA9]);
            i += 2;
        } else if p == 6 {
            buf[i..i + 3].copy_from_slice(&[0xE2, 0x82, 0xAC]);
            i += 3;
        } else {
            buf[i..i + 4].copy_from_slice(&[0xF0, 0x9F, 0x98, 0x80]);
            i += 4;
        }
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 5 {
        eprintln!("usage: strscan <variant> <size> <reps> <seed>");
        std::process::exit(2);
    }
    let name = args[1].as_str();
    let n: usize = args[2].parse().unwrap();
    let reps: usize = args[3].parse().unwrap();
    let seed: u64 = args[4].parse().unwrap();
    let mut buf = vec![0u8; n];
    fill(&mut buf, name == "isascii" || name == "lastindexbyte", seed);
    let mut acc: i64 = 0;
    match name {
        "isascii" => {
            for r in 0..reps {
                let off = r % 64;
                acc += buf[off..].is_ascii() as i64 + off as i64;
            }
        }
        "lastindexbyte" => {
            buf[100] = 0xFF;
            for r in 0..reps {
                let off = r % 64;
                let k = buf[off..].iter().rposition(|&c| c == 0xFF).map_or(-1, |k| k as i64);
                acc += k + off as i64;
            }
        }
        "utf16len" => {
            let s = std::str::from_utf8(&buf).unwrap();
            for r in 0..reps {
                let off = r % 64;
                acc += s[off..].encode_utf16().count() as i64 + off as i64;
            }
        }
        _ => {
            eprintln!("strscan: no std form in Rust for {}", name);
            std::process::exit(2);
        }
    }
    println!("{}", acc);
}
