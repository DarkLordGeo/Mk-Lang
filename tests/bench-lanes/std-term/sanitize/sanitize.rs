// The sanitize row (README.md): the same loop in Rust.
use std::env;

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

// generate writes n items into b (3 * n bytes, zeroed) and returns the
// length: indexed stores, as every column does.
fn generate(b: &mut [u8], n: i64) -> usize {
    let mut st: u64 = 1;
    let mut o = 0;
    for _ in 0..n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let z = mix(st);
        let k = z % 64;
        let v = ((z >> 8) % 95) as u8;
        let w = ((z >> 8) % 32) as u8;
        if k < 48 {
            b[o] = 0x20 + v;
            o += 1;
        } else if k < 52 {
            b[o] = 0x0a;
            o += 1;
        } else if k < 56 {
            b[o] = 0xc3;
            b[o + 1] = 0xa9;
            o += 2;
        } else if k < 58 {
            b[o] = 0x1b;
            o += 1;
        } else if k < 60 {
            b[o] = w;
            o += 1;
        } else if k < 62 {
            b[o] = 0xc2;
            b[o + 1] = 0x80 + w;
            o += 2;
        } else {
            b[o] = 0xe2;
            b[o + 1] = 0x82;
            b[o + 2] = 0xac;
            o += 3;
        }
    }
    o
}

// sanitize writes s to out with C0 controls but LF and tab, DEL and C1
// controls replaced by U+FFFD, each safe run copied whole, and returns the
// length: a fixed buffer written at an index, as sanitize.c's loop.
fn sanitize(out: &mut [u8], s: &[u8]) -> usize {
    let (mut o, mut run, mut i) = (0, 0, 0);
    while i < s.len() {
        let c = s[i];
        let bad = if (c < 0x20 && c != 0x0a && c != 0x09) || c == 0x7f {
            1
        } else if c == 0xc2 && i + 1 < s.len() && (0x80..=0x9f).contains(&s[i + 1]) {
            2
        } else {
            0
        };
        if bad == 0 {
            i += 1;
            continue;
        }
        out[o..o + (i - run)].copy_from_slice(&s[run..i]);
        o += i - run;
        out[o] = 0xef;
        out[o + 1] = 0xbf;
        out[o + 2] = 0xbd;
        o += 3;
        i += bad;
        run = i;
    }
    out[o..o + (s.len() - run)].copy_from_slice(&s[run..]);
    o + s.len() - run
}

fn main() {
    let a: Vec<String> = env::args().collect();
    let n: i64 = a[1].parse().unwrap();
    let rounds: i64 = a[2].parse().unwrap();
    let mut b = vec![0u8; 3 * n as usize];
    let len = generate(&mut b, n);
    let mut out = vec![0u8; 9 * n as usize];
    let mut total: u64 = 0;
    for r in 0..rounds {
        let m = sanitize(&mut out, &b[..len]);
        total = total.wrapping_add(m as u64);
        total = total.wrapping_add(out[((r * 7919) % m as i64) as usize] as u64);
    }
    println!("{}", total);
}
