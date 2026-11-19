// pipethru, Rust, std::process::Command: this program runs itself with
// "child n", which writes n bytes of a splitmix64 stream (each word's 8
// bytes, little endian, from state 0) to its standard output in 64 KiB
// writes; the parent reads the child's piped stdout with ChildStdout::read
// into a 64 KiB buffer and prints the bytes read, their sum and the
// child's exit code. stdin and stderr are Stdio::null(). Usage: pipethru <n>.
use std::io::{Read, Write};
use std::process::{Command, Stdio};

const CHUNK: usize = 65536;

fn fill(b: &mut [u8], mut x: u64) -> u64 {
    for w in b.chunks_exact_mut(8) {
        x = x.wrapping_add(0x9e3779b97f4a7c15);
        let mut z = x;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
        z ^= z >> 31;
        w.copy_from_slice(&z.to_le_bytes());
    }
    x
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args[1] == "child" {
        let n: usize = args[2].parse().unwrap();
        let mut b = vec![0u8; CHUNK];
        let mut out = std::io::stdout().lock();
        fill(&mut b, 0);
        let mut left = n;
        while left > 0 {
            let k = left.min(CHUNK);
            out.write_all(&b[..k]).unwrap();
            left -= k;
        }
        out.flush().unwrap();
        return;
    }
    let me = std::env::current_exe().unwrap();
    let mut c = Command::new(me)
        .arg("child")
        .arg(&args[1])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let mut r = c.stdout.take().unwrap();
    let mut b = vec![0u8; CHUNK];
    let (mut total, mut sum) = (0u64, 0u64);
    loop {
        let k = r.read(&mut b).unwrap();
        if k == 0 {
            break;
        }
        total += k as u64;
        for &v in &b[..k] {
            sum = sum.wrapping_add(v as u64);
        }
    }
    drop(r);
    let st = c.wait().unwrap();
    println!("{} {} {}", total, sum, st.code().unwrap_or(-1));
}
