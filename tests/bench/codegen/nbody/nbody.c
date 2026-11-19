// Codegen kernel nbody, C side (tests/bench/README.md "The codegen table";
// "The harness"). The same algorithm as
// nbody.mk's Fill and Run, and as ../../same/nbody: five bodies in seven
// [5]double arrays (structure of arrays); Fill (not timed) sets the stated
// initial state; Run (timed) advances it `steps` times with dt = 2^-7, every
// operation in the same order, then returns trunc(energy * 1e9). The state
// is constants, so the step count, which comes in at run time, is what keeps
// either optimizer from folding the run. No FMA (-ffp-contract=off; Mk
// never contracts); sqrt is __builtin_sqrt with -fno-math-errno, one
// sqrtsd, as Mk's math.Sqrt is. C has no bounds checks; Mk checks every
// s.x[i].
//
// Freestanding: no C library. tools/bench compiles it with clang 21
// (codegenCCommand in tools/bench/codegen.go) and links the object into the
// Mk program.
#include <stdint.h>

#define BODIES 5
static const double DT = 0.0078125;

struct state {
    double x[BODIES], y[BODIES], z[BODIES], vx[BODIES], vy[BODIES], vz[BODIES], m[BODIES];
};

static _Alignas(64) struct state s;

static const struct state initial = {
    .x = {0.0, 4.0, -2.0, 6.0, -9.0},
    .y = {0.0, 0.0, 0.0, 0.0, 0.0},
    .z = {0.0, 0.25, -0.125, 0.5, 0.0},
    .vx = {0.0, 0.0, 0.0, 0.0, 0.0},
    .vy = {0.0, 1.375, -2.0, 1.125, -0.9375},
    .vz = {0.0, 0.0, 0.0625, 0.0, 0.03125},
    .m = {8.0, 0.03125, 0.015625, 0.0625, 0.0078125},
};

static double energy(const struct state *p) {
    double e = 0.0;
    for (int64_t i = 0; i < BODIES; i++) {
        e = e + (0.5 * p->m[i]) * ((p->vx[i] * p->vx[i] + p->vy[i] * p->vy[i]) + p->vz[i] * p->vz[i]);
        for (int64_t j = i + 1; j < BODIES; j++) {
            double dx = p->x[i] - p->x[j];
            double dy = p->y[i] - p->y[j];
            double dz = p->z[i] - p->z[j];
            e = e - (p->m[i] * p->m[j]) / __builtin_sqrt((dx * dx + dy * dy) + dz * dz);
        }
    }
    return e;
}

void bench_c_nbody_fill(void) {
    s = initial;
}

int64_t bench_c_nbody_run(int64_t steps) {
    struct state *p = &s;
    for (int64_t k = 0; k < steps; k++) {
        for (int64_t i = 0; i < BODIES; i++) {
            for (int64_t j = i + 1; j < BODIES; j++) {
                double dx = p->x[i] - p->x[j];
                double dy = p->y[i] - p->y[j];
                double dz = p->z[i] - p->z[j];
                double d2 = (dx * dx + dy * dy) + dz * dz;
                double mag = DT / (d2 * __builtin_sqrt(d2));
                p->vx[i] = p->vx[i] - (dx * p->m[j]) * mag;
                p->vy[i] = p->vy[i] - (dy * p->m[j]) * mag;
                p->vz[i] = p->vz[i] - (dz * p->m[j]) * mag;
                p->vx[j] = p->vx[j] + (dx * p->m[i]) * mag;
                p->vy[j] = p->vy[j] + (dy * p->m[i]) * mag;
                p->vz[j] = p->vz[j] + (dz * p->m[i]) * mag;
            }
        }
        for (int64_t i = 0; i < BODIES; i++) {
            p->x[i] = p->x[i] + DT * p->vx[i];
            p->y[i] = p->y[i] + DT * p->vy[i];
            p->z[i] = p->z[i] + DT * p->vz[i];
        }
    }
    return (int64_t)(energy(p) * 1e9);
}
