// nbody, Rust: the reference algorithm of README.md.
// Five bodies in seven [f64; 5] arrays of one struct, indexed s.x[i] (bounds
// checked), every operation in the README's order; f64::sqrt (one sqrtsd);
// rustc never contracts a*b+c. `as i64` truncates toward zero. No unsafe.

const BODIES: usize = 5;
const DT: f64 = 0.0078125;

struct State {
    x: [f64; BODIES],
    y: [f64; BODIES],
    z: [f64; BODIES],
    vx: [f64; BODIES],
    vy: [f64; BODIES],
    vz: [f64; BODIES],
    m: [f64; BODIES],
}

fn energy(s: &State) -> f64 {
    let mut e = 0.0;
    for i in 0..BODIES {
        e = e + (0.5 * s.m[i]) * ((s.vx[i] * s.vx[i] + s.vy[i] * s.vy[i]) + s.vz[i] * s.vz[i]);
        for j in i + 1..BODIES {
            let dx = s.x[i] - s.x[j];
            let dy = s.y[i] - s.y[j];
            let dz = s.z[i] - s.z[j];
            e = e - (s.m[i] * s.m[j]) / ((dx * dx + dy * dy) + dz * dz).sqrt();
        }
    }
    e
}

fn advance(s: &mut State, steps: usize) {
    for _ in 0..steps {
        for i in 0..BODIES {
            for j in i + 1..BODIES {
                let dx = s.x[i] - s.x[j];
                let dy = s.y[i] - s.y[j];
                let dz = s.z[i] - s.z[j];
                let d2 = (dx * dx + dy * dy) + dz * dz;
                let mag = DT / (d2 * d2.sqrt());
                s.vx[i] = s.vx[i] - (dx * s.m[j]) * mag;
                s.vy[i] = s.vy[i] - (dy * s.m[j]) * mag;
                s.vz[i] = s.vz[i] - (dz * s.m[j]) * mag;
                s.vx[j] = s.vx[j] + (dx * s.m[i]) * mag;
                s.vy[j] = s.vy[j] + (dy * s.m[i]) * mag;
                s.vz[j] = s.vz[j] + (dz * s.m[i]) * mag;
            }
        }
        for i in 0..BODIES {
            s.x[i] = s.x[i] + DT * s.vx[i];
            s.y[i] = s.y[i] + DT * s.vy[i];
            s.z[i] = s.z[i] + DT * s.vz[i];
        }
    }
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 2 {
        eprintln!("usage: nbody <steps>");
        std::process::exit(2);
    }
    let steps: usize = a[1].parse().unwrap();
    let mut s = State {
        x: [0.0, 4.0, -2.0, 6.0, -9.0],
        y: [0.0, 0.0, 0.0, 0.0, 0.0],
        z: [0.0, 0.25, -0.125, 0.5, 0.0],
        vx: [0.0, 0.0, 0.0, 0.0, 0.0],
        vy: [0.0, 1.375, -2.0, 1.125, -0.9375],
        vz: [0.0, 0.0, 0.0625, 0.0, 0.03125],
        m: [8.0, 0.03125, 0.015625, 0.0625, 0.0078125],
    };
    advance(&mut s, steps);
    println!("{}", (energy(&s) * 1e9) as i64);
}
