// chan, Rust, idiomatic std: the task of README.md with two OS threads
// (std::thread::scope; Rust's std has threads, not tasks, so the cell is
// labelled threads) and two std::sync::mpsc::sync_channel(1): the main
// thread sends 0..n-1 on a and waits for each to come back on b; the other
// receives on a and sends the value back on b. Usage: chan <n>.
use std::sync::mpsc::sync_channel;

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 2 {
        eprintln!("usage: chan <n>");
        std::process::exit(2);
    }
    let n: i64 = a[1].parse().unwrap();
    let (atx, arx) = sync_channel::<i64>(1);
    let (btx, brx) = sync_channel::<i64>(1);
    let mut sum: i64 = 0;
    std::thread::scope(|s| {
        s.spawn(move || {
            for _ in 0..n {
                let x = arx.recv().unwrap();
                btx.send(x).unwrap();
            }
        });
        for i in 0..n {
            atx.send(i).unwrap();
            sum += brx.recv().unwrap();
        }
    });
    println!("{}", sum);
}
