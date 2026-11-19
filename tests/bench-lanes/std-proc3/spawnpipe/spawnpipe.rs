// spawnpipe, Rust, std::process::Command::spawn: n runs of this program
// with "child" (which copies standard input to standard output); each run
// gets 64 bytes on a piped stdin, which is then closed, its piped stdout
// read to the end, stderr Stdio::null(), and its exit waited for. Prints n,
// the bytes read back and the failed runs. Usage: spawnpipe <n>.
use std::io::{Read, Write};
use std::process::{Command, Stdio};

const PAYLOAD: &[u8] = b"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n";

fn once(me: &std::path::Path) -> Option<usize> {
    let mut c = Command::new(me)
        .arg("child")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    {
        let mut w = c.stdin.take()?;
        let _ = w.write_all(PAYLOAD);
    }
    let mut b = Vec::new();
    c.stdout.take()?.read_to_end(&mut b).ok()?;
    let st = c.wait().ok()?;
    if st.success() { Some(b.len()) } else { None }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args[1] == "child" {
        let mut b = Vec::new();
        std::io::stdin().read_to_end(&mut b).unwrap();
        std::io::stdout().write_all(&b).unwrap();
        return;
    }
    let n: u64 = args[1].parse().unwrap();
    let me = std::env::current_exe().unwrap();
    let (mut total, mut fails) = (0usize, 0u64);
    for _ in 0..n {
        match once(&me) {
            Some(64) => total += 64,
            _ => fails += 1,
        }
    }
    println!("{} {} {}", n, total, fails);
}
