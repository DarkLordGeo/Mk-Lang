// run, Rust, std::process::Command::output: n runs of this program with
// "child" (which writes 64 bytes to standard output and exits 0), standard
// output and standard error captured, standard input Stdio::null(), each
// waited for. Prints n, the bytes captured and the failed runs.
// Usage: run <n>.
use std::io::Write;
use std::process::{Command, Stdio};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args[1] == "child" {
        std::io::stdout().write_all(b"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n").unwrap();
        return;
    }
    let n: u64 = args[1].parse().unwrap();
    let me = std::env::current_exe().unwrap();
    let (mut total, mut fails) = (0usize, 0u64);
    for _ in 0..n {
        match Command::new(&me).arg("child").stdin(Stdio::null()).output() {
            Ok(o) if o.status.success() => total += o.stdout.len() + o.stderr.len(),
            _ => fails += 1,
        }
    }
    println!("{} {} {}", n, total, fails);
}
