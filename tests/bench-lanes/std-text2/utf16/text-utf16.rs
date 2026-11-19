// text-utf16, Rust: str::encode_utf16 collected into a Vec<u16> (encode),
// and String::from_utf16 of one unit buffer (decode), over the input and
// passes of mk/text-utf16.mk's comment (README.md). from_utf16 fails on an
// unpaired surrogate. Usage: text-utf16 <encode|decode> <mib> <reps> <seed>.

fn mix(x: u64) -> u64 {
    let mut z = x;
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn generate(n: usize, seed: u64) -> String {
    let mut s = String::new();
    let mut st = seed;
    while s.len() < n {
        st = st.wrapping_add(0x9e3779b97f4a7c15);
        let x = mix(st);
        let k = x % 16;
        let y = x >> 8;
        let c: u32 = if k < 10 {
            if x % 7 == 0 { 0x41 + (y % 26) as u32 } else { 0x61 + (y % 26) as u32 }
        } else if k == 10 {
            0x20
        } else if k == 11 {
            0xC0 + (y % 64) as u32
        } else if k == 12 {
            0x430 + (y % 32) as u32
        } else if k == 13 {
            0x4E00 + (y % 0x5000) as u32
        } else if k == 14 {
            0x1F600 + (y % 80) as u32
        } else {
            0x300 + (y % 0x70) as u32
        };
        s.push(char::from_u32(c).unwrap());
    }
    s
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 5 {
        eprintln!("usage: text-utf16 <encode|decode> <mib> <reps> <seed>");
        std::process::exit(1);
    }
    let mode = args[1].as_str();
    let mib: usize = args[2].parse().unwrap();
    let reps: usize = args[3].parse().unwrap();
    let seed: u64 = args[4].parse().unwrap();
    let s = generate(mib * 1048576, seed);
    let mut sum: u64 = 0;
    match mode {
        "encode" => {
            for r in 0..reps {
                let u: Vec<u16> = s.encode_utf16().collect();
                sum = sum.wrapping_add(u.len() as u64).wrapping_add(u[r % u.len()] as u64);
            }
        }
        "decode" => {
            let u: Vec<u16> = s.encode_utf16().collect();
            for r in 0..reps {
                let back = String::from_utf16(&u).unwrap();
                sum = sum
                    .wrapping_add(back.len() as u64)
                    .wrapping_add(back.as_bytes()[r % back.len()] as u64);
            }
        }
        _ => {
            eprintln!("mode is encode or decode");
            std::process::exit(1);
        }
    }
    println!("{}", sum);
}
