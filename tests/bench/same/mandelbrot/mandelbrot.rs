// mandelbrot, Rust: the reference algorithm of README.md.
// Scalar f64, one point at a time, at most 50 iterations, the escape test
// after each step, the README's parenthesisation (rustc never contracts).
// i64 counters: + wraps in "rust" and traps in "rust-ovf". No unsafe.

const MAX_ITER: i64 = 50;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        eprintln!("usage: mandelbrot <n>");
        std::process::exit(2);
    }
    let n: i64 = args[1].parse().unwrap();
    let fn_ = n as f64;
    let mut total: i64 = 0;
    for y in 0..n {
        let ci = (2.0 * y as f64) / fn_ - 1.0;
        for x in 0..n {
            let cr = (2.0 * x as f64) / fn_ - 1.5;
            let mut zr = 0.0;
            let mut zi = 0.0;
            let mut i: i64 = 0;
            while i < MAX_ITER {
                let tr = (zr * zr - zi * zi) + cr;
                let ti = (2.0 * zr) * zi + ci;
                zr = tr;
                zi = ti;
                if zr * zr + zi * zi > 4.0 {
                    break;
                }
                i += 1;
            }
            total += i;
        }
    }
    println!("{}", total);
}
