// stat, Rust: the facts of one existing file, n times, through
// std::fs::metadata (following links); sum adds each answer's len. Prints
// n, sum and the failed calls. Usage: stat <path> <n>.
fn main() {
    let args: Vec<String> = std::env::args().collect();
    let p = &args[1];
    let n: u64 = args[2].parse().unwrap();
    let mut sum: u64 = 0;
    let mut fails = 0;
    for _ in 0..n {
        match std::fs::metadata(p) {
            Ok(m) => sum += m.len(),
            Err(_) => fails += 1,
        }
    }
    println!("{} {} {}", n, sum, fails);
}
