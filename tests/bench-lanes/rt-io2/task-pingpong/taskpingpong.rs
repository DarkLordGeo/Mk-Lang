// task-pingpong, Rust: two threads (Rust's std has no task runtime) pass a
// value back and forth over two rendezvous channels (sync_channel(0)); each
// of n rounds sends i on ping, the partner answers i*3+1 on pong; sum = 31
// sum + the answer (wrapping). Prints n, sum and 0. Usage: taskpingpong <n>.
use std::sync::mpsc::sync_channel;
use std::thread;

fn main() {
    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();
    let (ping_tx, ping_rx) = sync_channel::<u64>(0);
    let (pong_tx, pong_rx) = sync_channel::<u64>(0);
    let partner = thread::spawn(move || {
        for _ in 0..n {
            let v = ping_rx.recv().unwrap();
            pong_tx.send(v * 3 + 1).unwrap();
        }
    });
    let mut sum: u64 = 0;
    for i in 0..n {
        ping_tx.send(i).unwrap();
        sum = sum.wrapping_mul(31).wrapping_add(pong_rx.recv().unwrap());
    }
    partner.join().unwrap();
    println!("{} {} 0", n, sum);
}
