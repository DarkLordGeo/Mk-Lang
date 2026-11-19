// nbody, C: the reference algorithm of README.md.
// Five bodies in seven double[5] arrays of one struct (structure of arrays),
// every operation in the README's order and parenthesisation; sqrt from
// <math.h> (one sqrtsd); -ffp-contract=off so no a*b+c is fused.
// Built by tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
// -fms-runtime-lib=static -fuse-ld=lld.
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define BODIES 5
static const double DT = 0.0078125;

struct state {
    double x[BODIES], y[BODIES], z[BODIES], vx[BODIES], vy[BODIES], vz[BODIES], m[BODIES];
};

static double energy(const struct state *s) {
    double e = 0.0;
    for (int64_t i = 0; i < BODIES; i++) {
        e = e + (0.5 * s->m[i]) * ((s->vx[i] * s->vx[i] + s->vy[i] * s->vy[i]) + s->vz[i] * s->vz[i]);
        for (int64_t j = i + 1; j < BODIES; j++) {
            double dx = s->x[i] - s->x[j];
            double dy = s->y[i] - s->y[j];
            double dz = s->z[i] - s->z[j];
            e = e - (s->m[i] * s->m[j]) / sqrt((dx * dx + dy * dy) + dz * dz);
        }
    }
    return e;
}

static void advance(struct state *s, int64_t steps) {
    for (int64_t k = 0; k < steps; k++) {
        for (int64_t i = 0; i < BODIES; i++) {
            for (int64_t j = i + 1; j < BODIES; j++) {
                double dx = s->x[i] - s->x[j];
                double dy = s->y[i] - s->y[j];
                double dz = s->z[i] - s->z[j];
                double d2 = (dx * dx + dy * dy) + dz * dz;
                double mag = DT / (d2 * sqrt(d2));
                s->vx[i] = s->vx[i] - (dx * s->m[j]) * mag;
                s->vy[i] = s->vy[i] - (dy * s->m[j]) * mag;
                s->vz[i] = s->vz[i] - (dz * s->m[j]) * mag;
                s->vx[j] = s->vx[j] + (dx * s->m[i]) * mag;
                s->vy[j] = s->vy[j] + (dy * s->m[i]) * mag;
                s->vz[j] = s->vz[j] + (dz * s->m[i]) * mag;
            }
        }
        for (int64_t i = 0; i < BODIES; i++) {
            s->x[i] = s->x[i] + DT * s->vx[i];
            s->y[i] = s->y[i] + DT * s->vy[i];
            s->z[i] = s->z[i] + DT * s->vz[i];
        }
    }
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: nbody <steps>\n");
        return 2;
    }
    int64_t steps = strtoll(argv[1], NULL, 10);
    struct state s = {
        .x = {0.0, 4.0, -2.0, 6.0, -9.0},
        .y = {0.0, 0.0, 0.0, 0.0, 0.0},
        .z = {0.0, 0.25, -0.125, 0.5, 0.0},
        .vx = {0.0, 0.0, 0.0, 0.0, 0.0},
        .vy = {0.0, 1.375, -2.0, 1.125, -0.9375},
        .vz = {0.0, 0.0, 0.0625, 0.0, 0.03125},
        .m = {8.0, 0.03125, 0.015625, 0.0625, 0.0078125},
    };
    advance(&s, steps);
    printf("%lld\n", (long long)(int64_t)(energy(&s) * 1e9));
    return 0;
}
