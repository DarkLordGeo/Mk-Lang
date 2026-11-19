// spawn, Rust, std::process::Command: n runs of this program with "child"
// (which exits 0 at once), every stream Stdio::null(), each waited for.
// Prints n, the sum of the exit codes and the failed spawns.
// Usage: spawn <n>.
use std::process::{Command, Stdio};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args[1] == "child" {
        return;
    }
    let n: u64 = args[1].parse().unwrap();
    let me = std::env::current_exe().unwrap();
    let (mut sum, mut fails) = (0i64, 0u64);
    for _ in 0..n {
        match Command::new(&me).arg("child").stdin(Stdio::null()).stdout(Stdio::null()).stderr(Stdio::null()).status() {
            Ok(s) => sum += s.code().unwrap_or(-1) as i64,
            Err(_) => fails += 1,
        }
    }
    println!("{} {} {}", n, sum, fails);
}
