// cancelpoint, Rust: n uncontended lock and unlock pairs of a
// std::sync::Mutex, each followed by a load of this loop's cancel flag (an
// AtomicBool: Rust's std has no scopes or cancellation, so a flag the
// caller checks is the idiom), the guarded value updated as
// x = x * 31 + i (wrapping). With expired 1 another flag, an unrelated
// one, is set before the loop; this loop never reads it. Prints n, x and
// the failed calls. Usage: cancelpoint <n> <expired>.
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;

static OURS: AtomicBool = AtomicBool::new(false);
static OTHER: AtomicBool = AtomicBool::new(false);

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let n: u64 = args[1].parse().unwrap();
    let expired: u32 = args[2].parse().unwrap();
    if expired == 1 {
        OTHER.store(true, Ordering::SeqCst);
    }
    let m = Mutex::new(0u64);
    let mut fails = 0u64;
    for i in 0..n {
        let mut g = m.lock().unwrap();
        if OURS.load(Ordering::SeqCst) {
            fails += 1;
            break;
        }
        *g = g.wrapping_mul(31).wrapping_add(i);
    }
    let x = *m.lock().unwrap();
    println!("{} {} {}", n, x, fails);
}
