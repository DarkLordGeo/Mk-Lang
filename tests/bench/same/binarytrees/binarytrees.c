// binarytrees, C: the reference algorithm of README.md (the Benchmarks Game's
// binary-trees, with the row's bump allocator). Nodes are struct node { uint32_t left,
// right; } in one array from calloc, allocated by a uint32_t bump index in a
// struct pool passed by pointer, as in every column (no file-scope state);
// index 0 is the null node. No malloc per node, no free, no bounds checks.
// The bump index is unsigned, so its overflow would wrap (never reached).
// Built by tools/bench with clang 21: -O3 -march=x86-64-v2 -ffp-contract=off
// -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

struct node {
    uint32_t left, right;
};

struct pool {
    struct node *nodes;
    uint32_t top;
};

static uint32_t build(struct pool *p, int64_t depth) {
    uint32_t idx = p->top;
    p->top += 1;
    if (depth > 0) {
        uint32_t l = build(p, depth - 1);
        uint32_t r = build(p, depth - 1);
        p->nodes[idx] = (struct node){l, r};
    } else {
        p->nodes[idx] = (struct node){0, 0};
    }
    return idx;
}

static int64_t check(const struct node *nodes, uint32_t idx) {
    struct node n = nodes[idx];
    if (n.left == 0) {
        return 1;
    }
    return 1 + check(nodes, n.left) + check(nodes, n.right);
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: binarytrees <n>\n");
        return 2;
    }
    int64_t n = strtoll(argv[1], NULL, 10);
    int64_t minDepth = 4;
    int64_t maxDepth = n;
    if (minDepth + 2 > maxDepth) {
        maxDepth = minDepth + 2;
    }
    struct pool p;
    p.nodes = calloc(((size_t)1 << (maxDepth + 2)) + 1, sizeof(struct node));
    if (p.nodes == NULL) {
        return 1;
    }
    p.top = 1;
    uint32_t t = build(&p, maxDepth + 1);
    int64_t total = check(p.nodes, t);
    p.top = 1;
    uint32_t longLived = build(&p, maxDepth);
    uint32_t mark = p.top;
    for (int64_t d = minDepth; d <= maxDepth; d += 2) {
        int64_t iterations = (int64_t)1 << (maxDepth - d + minDepth);
        for (int64_t i = 0; i < iterations; i++) {
            p.top = mark;
            t = build(&p, d);
            total += check(p.nodes, t);
        }
    }
    total += check(p.nodes, longLived);
    printf("%lld\n", (long long)total);
    free(p.nodes);
    return 0;
}
