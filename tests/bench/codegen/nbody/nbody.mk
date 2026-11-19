// Codegen kernel nbody, Mk side, with the in-process harness
// (tests/bench/README.md "The codegen table"). Fill and Run are the
// algorithm of nbody.c; Main alternates the two sides and prints one line
// per round, which tools/bench reads (median, minimum and MAD per side).
// The state is a struct of seven [5]f64 arrays held in Main (no buffer to
// align); every s.x[i] is bounds checked, the loop counters' + is checked.
// The timed region is Advance and Energy, as bench_c_nbody_run.
// Usage: nbody <steps> <rounds>.

import std.math
import std.time
import ref

const Bodies: int = 5
const Dt: f64 = 0.0078125

struct State {
    x: [5]f64
    y: [5]f64
    z: [5]f64
    vx: [5]f64
    vy: [5]f64
    vz: [5]f64
    m: [5]f64
}

Fill(s: mut State) {
    s.x = [0.0, 4.0, -2.0, 6.0, -9.0]
    s.y = [0.0, 0.0, 0.0, 0.0, 0.0]
    s.z = [0.0, 0.25, -0.125, 0.5, 0.0]
    s.vx = [0.0, 0.0, 0.0, 0.0, 0.0]
    s.vy = [0.0, 1.375, -2.0, 1.125, -0.9375]
    s.vz = [0.0, 0.0, 0.0625, 0.0, 0.03125]
    s.m = [8.0, 0.03125, 0.015625, 0.0625, 0.0078125]
}

Energy(s: &State): f64 {
    var e := 0.0
    for i in 0..Bodies {
        e = e + (0.5 * s.m[i]) * ((s.vx[i] * s.vx[i] + s.vy[i] * s.vy[i]) + s.vz[i] * s.vz[i])
        for j in i + 1..Bodies {
            dx := s.x[i] - s.x[j]
            dy := s.y[i] - s.y[j]
            dz := s.z[i] - s.z[j]
            e = e - (s.m[i] * s.m[j]) / math.Sqrt((dx * dx + dy * dy) + dz * dz)
        }
    }
    return e
}

Advance(s: mut State, steps: int) {
    for _ in 0..steps {
        for i in 0..Bodies {
            for j in i + 1..Bodies {
                dx := s.x[i] - s.x[j]
                dy := s.y[i] - s.y[j]
                dz := s.z[i] - s.z[j]
                d2 := (dx * dx + dy * dy) + dz * dz
                mag := Dt / (d2 * math.Sqrt(d2))
                s.vx[i] = s.vx[i] - (dx * s.m[j]) * mag
                s.vy[i] = s.vy[i] - (dy * s.m[j]) * mag
                s.vz[i] = s.vz[i] - (dz * s.m[j]) * mag
                s.vx[j] = s.vx[j] + (dx * s.m[i]) * mag
                s.vy[j] = s.vy[j] + (dy * s.m[i]) * mag
                s.vz[j] = s.vz[j] + (dz * s.m[i]) * mag
            }
        }
        for i in 0..Bodies {
            s.x[i] = s.x[i] + Dt * s.vx[i]
            s.y[i] = s.y[i] + Dt * s.vy[i]
            s.z[i] = s.z[i] + Dt * s.vz[i]
        }
    }
}

export Main(sys: &System): ParseError|WriteError!void {
    steps := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    var s := State(
        x: [0.0; 5],
        y: [0.0; 5],
        z: [0.0; 5],
        vx: [0.0; 5],
        vy: [0.0; 5],
        vz: [0.0; 5],
        m: [0.0; 5],
    )
    for round in 0..rounds {
        var mk_ns: i64 = 0
        var c_ns: i64 = 0
        var mk_sum: i64 = 0
        var c_sum: i64 = 0
        for side in 0..2 {
            if (side + round) % 2 == 0 {
                Fill(mut s)
                t0 := time.Now(sys.io)
                Advance(mut s, steps)
                mk_sum = Cast[i64](Energy(&s) * 1e9)
                mk_ns = time.Now(sys.io).Since(t0).Nanos()
            } else {
                ref.CFill()
                t0 := time.Now(sys.io)
                c_sum = ref.CRun(steps)
                c_ns = time.Now(sys.io).Since(t0).Nanos()
            }
        }
        try Print(mut out, "round {round} mk {mk_ns} {mk_sum} c {c_ns} {c_sum}\n", (round, mk_ns, mk_sum, c_ns, c_sum))
    }
}
