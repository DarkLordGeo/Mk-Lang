// sortints, C: the reference algorithm of README.md.
// Go 1.27's pdqsortOrdered (slices/zsortordered.go) written out for int64_t
// and <, not qsort: the same functions, constants and loop structure as every
// other column. The slice is a pointer and two int64_t indices; no bounds
// checks (C has none). Index arithmetic is signed int64_t (overflow is UB,
// which no input of the stated sizes reaches); the fill's splitmix64 is
// uint64_t, wrapping by design. Built by tools/bench with clang 21:
// -O3 -march=x86-64-v2 -ffp-contract=off -fms-runtime-lib=static -fuse-ld=lld.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef int64_t i64;

// -DBENCH_COUNT=1 builds the counting variant (tests/bench/README.md);
// the counters are dead code otherwise.
#ifndef BENCH_COUNT
#define BENCH_COUNT 0
#endif
static uint64_t n_pdq, n_ins, n_shift, n_heap, n_rev, n_part, n_parteq, n_pis, n_brk;

static uint64_t mix(uint64_t z) {
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9u;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebu;
    return z ^ (z >> 31);
}

static i64 bitlen(i64 n) {
    i64 l = 0;
    for (uint64_t x = (uint64_t)n; x > 0; x >>= 1) {
        l++;
    }
    return l;
}

static void insertion_sort(i64 *s, i64 a, i64 b) {
    if (BENCH_COUNT) {
        n_ins += 1;
    }
    for (i64 i = a + 1; i < b; i++) {
        for (i64 j = i; j > a && s[j] < s[j - 1]; j--) {
            i64 t = s[j];
            s[j] = s[j - 1];
            s[j - 1] = t;
            if (BENCH_COUNT) {
                n_shift += 1;
            }
        }
    }
}

static void sift_down(i64 *s, i64 lo, i64 hi, i64 first) {
    i64 root = lo;
    for (;;) {
        i64 child = 2 * root + 1;
        if (child >= hi) {
            return;
        }
        if (child + 1 < hi && s[first + child] < s[first + child + 1]) {
            child++;
        }
        if (!(s[first + root] < s[first + child])) {
            return;
        }
        i64 t = s[first + root];
        s[first + root] = s[first + child];
        s[first + child] = t;
        root = child;
    }
}

static void heap_sort(i64 *s, i64 a, i64 b) {
    if (BENCH_COUNT) {
        n_heap += 1;
    }
    i64 first = a;
    i64 hi = b - a;
    for (i64 i = (hi - 1) / 2; i >= 0; i--) {
        sift_down(s, i, hi, first);
    }
    for (i64 i = hi - 1; i >= 0; i--) {
        i64 t = s[first];
        s[first] = s[first + i];
        s[first + i] = t;
        sift_down(s, 0, i, first);
    }
}

static i64 median(i64 *s, i64 a, i64 b, i64 c, i64 *swaps) {
    i64 x = a, y = b, z = c;
    if (s[y] < s[x]) {
        *swaps += 1;
        i64 t = x;
        x = y;
        y = t;
    }
    if (s[z] < s[y]) {
        *swaps += 1;
        i64 t = y;
        y = z;
        z = t;
    }
    if (s[y] < s[x]) {
        *swaps += 1;
        i64 t = x;
        x = y;
        y = t;
    }
    (void)z;
    return y;
}

// choose_pivot returns the pivot; *hint is 0 unknown, 1 increasing, 2 decreasing.
static i64 choose_pivot(i64 *s, i64 a, i64 b, i64 *hint) {
    i64 l = b - a;
    i64 swaps = 0;
    i64 i = a + l / 4 * 1;
    i64 j = a + l / 4 * 2;
    i64 k = a + l / 4 * 3;
    if (l >= 8) {
        if (l >= 50) {
            i = median(s, i - 1, i, i + 1, &swaps);
            j = median(s, j - 1, j, j + 1, &swaps);
            k = median(s, k - 1, k, k + 1, &swaps);
        }
        j = median(s, i, j, k, &swaps);
    }
    if (swaps == 0) {
        *hint = 1;
    } else if (swaps == 12) {
        *hint = 2;
    } else {
        *hint = 0;
    }
    return j;
}

static void reverse_range(i64 *s, i64 a, i64 b) {
    if (BENCH_COUNT) {
        n_rev += 1;
    }
    i64 i = a, j = b - 1;
    while (i < j) {
        i64 t = s[i];
        s[i] = s[j];
        s[j] = t;
        i++;
        j--;
    }
}

static int partial_insertion_sort(i64 *s, i64 a, i64 b) {
    if (BENCH_COUNT) {
        n_pis += 1;
    }
    i64 i = a + 1;
    for (int step = 0; step < 5; step++) {
        while (i < b && !(s[i] < s[i - 1])) {
            i++;
        }
        if (i == b) {
            return 1;
        }
        if (b - a < 50) {
            return 0;
        }
        i64 t = s[i];
        s[i] = s[i - 1];
        s[i - 1] = t;
        if (i - a >= 2) {
            for (i64 j = i - 1; j >= 1; j--) {
                if (!(s[j] < s[j - 1])) {
                    break;
                }
                i64 u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
            }
        }
        if (b - i >= 2) {
            for (i64 j = i + 1; j < b; j++) {
                if (!(s[j] < s[j - 1])) {
                    break;
                }
                i64 u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
            }
        }
    }
    return 0;
}

static void break_patterns(i64 *s, i64 a, i64 b) {
    if (BENCH_COUNT) {
        n_brk += 1;
    }
    i64 length = b - a;
    if (length < 8) {
        return;
    }
    uint64_t r = (uint64_t)length;
    uint64_t modulus = (uint64_t)1 << bitlen(length);
    i64 idx = a + (length / 4) * 2 - 1;
    for (i64 i = 0; i < 3; i++) {
        r ^= r << 13;
        r ^= r >> 7;
        r ^= r << 17;
        i64 other = (i64)(r & (modulus - 1));
        if (other >= length) {
            other -= length;
        }
        i64 t = s[idx + i];
        s[idx + i] = s[a + other];
        s[a + other] = t;
    }
}

static i64 partition(i64 *s, i64 a, i64 b, i64 pivot, int *already) {
    if (BENCH_COUNT) {
        n_part += 1;
    }
    i64 t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    i64 i = a + 1, j = b - 1;
    while (i <= j && s[i] < s[a]) {
        i++;
    }
    while (i <= j && !(s[j] < s[a])) {
        j--;
    }
    if (i > j) {
        t = s[j];
        s[j] = s[a];
        s[a] = t;
        *already = 1;
        return j;
    }
    t = s[i];
    s[i] = s[j];
    s[j] = t;
    i++;
    j--;
    for (;;) {
        while (i <= j && s[i] < s[a]) {
            i++;
        }
        while (i <= j && !(s[j] < s[a])) {
            j--;
        }
        if (i > j) {
            break;
        }
        t = s[i];
        s[i] = s[j];
        s[j] = t;
        i++;
        j--;
    }
    t = s[j];
    s[j] = s[a];
    s[a] = t;
    *already = 0;
    return j;
}

static i64 partition_equal(i64 *s, i64 a, i64 b, i64 pivot) {
    if (BENCH_COUNT) {
        n_parteq += 1;
    }
    i64 t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    i64 i = a + 1, j = b - 1;
    for (;;) {
        while (i <= j && !(s[a] < s[i])) {
            i++;
        }
        while (i <= j && s[a] < s[j]) {
            j--;
        }
        if (i > j) {
            break;
        }
        t = s[i];
        s[i] = s[j];
        s[j] = t;
        i++;
        j--;
    }
    return i;
}

static void pdqsort(i64 *s, i64 a, i64 b, i64 limit) {
    int balanced = 1;
    int partitioned = 1;
    for (;;) {
        i64 length = b - a;
        if (BENCH_COUNT) {
            n_pdq += 1;
        }
        if (length <= 12) {
            insertion_sort(s, a, b);
            return;
        }
        if (limit == 0) {
            heap_sort(s, a, b);
            return;
        }
        if (!balanced) {
            break_patterns(s, a, b);
            limit--;
        }
        i64 hint = 0;
        i64 pivot = choose_pivot(s, a, b, &hint);
        if (hint == 2) {
            reverse_range(s, a, b);
            pivot = (b - 1) - (pivot - a);
            hint = 1;
        }
        if (balanced && partitioned && hint == 1) {
            if (partial_insertion_sort(s, a, b)) {
                return;
            }
        }
        if (a > 0 && !(s[a - 1] < s[pivot])) {
            a = partition_equal(s, a, b, pivot);
            continue;
        }
        int already = 0;
        i64 mid = partition(s, a, b, pivot, &already);
        partitioned = already;
        i64 left = mid - a, right = b - mid;
        i64 threshold = length / 8;
        if (left < right) {
            balanced = left >= threshold;
            pdqsort(s, a, mid, limit);
            a = mid + 1;
        } else {
            balanced = right >= threshold;
            pdqsort(s, mid + 1, b, limit);
            b = mid;
        }
    }
}

static i64 check(const i64 *s, i64 n) {
    i64 sum = 0;
    for (i64 i = 0; i < n; i++) {
        if (i > 0 && s[i - 1] > s[i]) {
            return -1;
        }
        sum += s[i] * (i % 1000);
    }
    return sum;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: sortints <n> <rounds> <seed>\n");
        return 2;
    }
    i64 n = strtoll(argv[1], NULL, 10);
    i64 rounds = strtoll(argv[2], NULL, 10);
    uint64_t st = strtoull(argv[3], NULL, 10);
    i64 *xs = malloc((size_t)n * sizeof *xs);
    if (xs == NULL) {
        return 1;
    }
    i64 total = 0;
    for (i64 r = 0; r < rounds; r++) {
        for (i64 p = 0; p < 4; p++) {
            for (i64 i = 0; i < n; i++) {
                if (p == 0) {
                    st += 0x9e3779b97f4a7c15u;
                    xs[i] = (i64)(mix(st) & 0xFFFFF);
                } else if (p == 1) {
                    xs[i] = n - i;
                } else if (p == 2) {
                    xs[i] = i % 1000;
                } else {
                    st += 0x9e3779b97f4a7c15u;
                    xs[i] = (i64)(mix(st) >> 60);
                }
            }
            pdqsort(xs, 0, n, bitlen(n));
            total += check(xs, n);
        }
    }
    printf("%lld\n", (long long)total);
    if (BENCH_COUNT) {
        printf("counts pdq=%llu ins=%llu shift=%llu heap=%llu rev=%llu part=%llu parteq=%llu pis=%llu brk=%llu\n", (unsigned long long)n_pdq, (unsigned long long)n_ins, (unsigned long long)n_shift, (unsigned long long)n_heap, (unsigned long long)n_rev, (unsigned long long)n_part, (unsigned long long)n_parteq, (unsigned long long)n_pis, (unsigned long long)n_brk);
    }
    free(xs);
    return 0;
}
