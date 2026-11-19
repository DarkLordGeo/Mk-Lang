// sortints, Rust: the reference algorithm of README.md.
// Go 1.27's pdqsortOrdered (slices/zsortordered.go) written out for &mut [i64]
// and <, not slice::sort_unstable: the same functions, constants and loop
// structure as every other column. Indices are usize (Rust indexes with it;
// no index of the stated sizes comes near its range, and no subtraction in
// the algorithm goes below zero). Every s[i] is bounds checked (the optimizer
// may remove the checks it proves); no unchecked indexing, no unsafe, no crates.
// Keys and the checksum are i64 with plain + and *: they wrap in the "rust"
// column and trap in "rust-ovf"; overflow class "never". The fill uses
// wrapping_* because splitmix64 wraps by design.

// --cfg bench_count builds the counting variant (tests/bench/README.md);
// the counters are dead code otherwise.
use std::sync::atomic::{AtomicU64, Ordering::Relaxed};
static N_PDQ: AtomicU64 = AtomicU64::new(0);
static N_INS: AtomicU64 = AtomicU64::new(0);
static N_SHIFT: AtomicU64 = AtomicU64::new(0);
static N_HEAP: AtomicU64 = AtomicU64::new(0);
static N_REV: AtomicU64 = AtomicU64::new(0);
static N_PART: AtomicU64 = AtomicU64::new(0);
static N_PARTEQ: AtomicU64 = AtomicU64::new(0);
static N_PIS: AtomicU64 = AtomicU64::new(0);
static N_BRK: AtomicU64 = AtomicU64::new(0);

fn mix(mut z: u64) -> u64 {
    z = (z ^ (z >> 30)).wrapping_mul(0xbf58476d1ce4e5b9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94d049bb133111eb);
    z ^ (z >> 31)
}

fn bitlen(n: usize) -> usize {
    let mut l = 0;
    let mut x = n as u64;
    while x > 0 {
        l += 1;
        x >>= 1;
    }
    l
}

fn insertion_sort(s: &mut [i64], a: usize, b: usize) {
    if cfg!(bench_count) {
        N_INS.fetch_add(1, Relaxed);
    }
    for i in a + 1..b {
        let mut j = i;
        while j > a && s[j] < s[j - 1] {
            let t = s[j];
            s[j] = s[j - 1];
            s[j - 1] = t;
            if cfg!(bench_count) {
                N_SHIFT.fetch_add(1, Relaxed);
            }
            j -= 1;
        }
    }
}

fn sift_down(s: &mut [i64], lo: usize, hi: usize, first: usize) {
    let mut root = lo;
    loop {
        let mut child = 2 * root + 1;
        if child >= hi {
            return;
        }
        if child + 1 < hi && s[first + child] < s[first + child + 1] {
            child += 1;
        }
        if !(s[first + root] < s[first + child]) {
            return;
        }
        let t = s[first + root];
        s[first + root] = s[first + child];
        s[first + child] = t;
        root = child;
    }
}

fn heap_sort(s: &mut [i64], a: usize, b: usize) {
    if cfg!(bench_count) {
        N_HEAP.fetch_add(1, Relaxed);
    }
    let first = a;
    let hi = b - a;
    let mut i = (hi - 1) / 2;
    loop {
        sift_down(s, i, hi, first);
        if i == 0 {
            break;
        }
        i -= 1;
    }
    let mut i = hi - 1;
    loop {
        let t = s[first];
        s[first] = s[first + i];
        s[first + i] = t;
        sift_down(s, 0, i, first);
        if i == 0 {
            break;
        }
        i -= 1;
    }
}

fn median(s: &[i64], a: usize, b: usize, c: usize, swaps: &mut i64) -> usize {
    let (mut x, mut y, mut z) = (a, b, c);
    if s[y] < s[x] {
        *swaps += 1;
        std::mem::swap(&mut x, &mut y);
    }
    if s[z] < s[y] {
        *swaps += 1;
        std::mem::swap(&mut y, &mut z);
    }
    if s[y] < s[x] {
        *swaps += 1;
        std::mem::swap(&mut x, &mut y);
    }
    let _ = (x, z);
    y
}

// choose_pivot returns the pivot and the hint: 0 unknown, 1 increasing, 2 decreasing.
fn choose_pivot(s: &[i64], a: usize, b: usize) -> (usize, i64) {
    let l = b - a;
    let mut swaps: i64 = 0;
    let mut i = a + l / 4 * 1;
    let mut j = a + l / 4 * 2;
    let mut k = a + l / 4 * 3;
    if l >= 8 {
        if l >= 50 {
            i = median(s, i - 1, i, i + 1, &mut swaps);
            j = median(s, j - 1, j, j + 1, &mut swaps);
            k = median(s, k - 1, k, k + 1, &mut swaps);
        }
        j = median(s, i, j, k, &mut swaps);
    }
    match swaps {
        0 => (j, 1),
        12 => (j, 2),
        _ => (j, 0),
    }
}

fn reverse_range(s: &mut [i64], a: usize, b: usize) {
    if cfg!(bench_count) {
        N_REV.fetch_add(1, Relaxed);
    }
    let mut i = a;
    let mut j = b - 1;
    while i < j {
        let t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
}

fn partial_insertion_sort(s: &mut [i64], a: usize, b: usize) -> bool {
    if cfg!(bench_count) {
        N_PIS.fetch_add(1, Relaxed);
    }
    let mut i = a + 1;
    for _ in 0..5 {
        while i < b && !(s[i] < s[i - 1]) {
            i += 1;
        }
        if i == b {
            return true;
        }
        if b - a < 50 {
            return false;
        }
        let t = s[i];
        s[i] = s[i - 1];
        s[i - 1] = t;
        if i - a >= 2 {
            let mut j = i - 1;
            while j >= 1 {
                if !(s[j] < s[j - 1]) {
                    break;
                }
                let u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
                j -= 1;
            }
        }
        if b - i >= 2 {
            let mut j = i + 1;
            while j < b {
                if !(s[j] < s[j - 1]) {
                    break;
                }
                let u = s[j];
                s[j] = s[j - 1];
                s[j - 1] = u;
                j += 1;
            }
        }
    }
    false
}

fn break_patterns(s: &mut [i64], a: usize, b: usize) {
    if cfg!(bench_count) {
        N_BRK.fetch_add(1, Relaxed);
    }
    let length = b - a;
    if length < 8 {
        return;
    }
    let mut r = length as u64;
    let modulus = 1u64 << bitlen(length);
    let idx = a + (length / 4) * 2 - 1;
    for i in 0..3 {
        r ^= r << 13;
        r ^= r >> 7;
        r ^= r << 17;
        let mut other = (r & (modulus - 1)) as usize;
        if other >= length {
            other -= length;
        }
        let t = s[idx + i];
        s[idx + i] = s[a + other];
        s[a + other] = t;
    }
}

fn partition(s: &mut [i64], a: usize, b: usize, pivot: usize) -> (usize, bool) {
    if cfg!(bench_count) {
        N_PART.fetch_add(1, Relaxed);
    }
    let t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    let mut i = a + 1;
    let mut j = b - 1;
    while i <= j && s[i] < s[a] {
        i += 1;
    }
    while i <= j && !(s[j] < s[a]) {
        j -= 1;
    }
    if i > j {
        let t = s[j];
        s[j] = s[a];
        s[a] = t;
        return (j, true);
    }
    let t = s[i];
    s[i] = s[j];
    s[j] = t;
    i += 1;
    j -= 1;
    loop {
        while i <= j && s[i] < s[a] {
            i += 1;
        }
        while i <= j && !(s[j] < s[a]) {
            j -= 1;
        }
        if i > j {
            break;
        }
        let t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
    let t = s[j];
    s[j] = s[a];
    s[a] = t;
    (j, false)
}

fn partition_equal(s: &mut [i64], a: usize, b: usize, pivot: usize) -> usize {
    if cfg!(bench_count) {
        N_PARTEQ.fetch_add(1, Relaxed);
    }
    let t = s[a];
    s[a] = s[pivot];
    s[pivot] = t;
    let mut i = a + 1;
    let mut j = b - 1;
    loop {
        while i <= j && !(s[a] < s[i]) {
            i += 1;
        }
        while i <= j && s[a] < s[j] {
            j -= 1;
        }
        if i > j {
            break;
        }
        let t = s[i];
        s[i] = s[j];
        s[j] = t;
        i += 1;
        j -= 1;
    }
    i
}

fn pdqsort(s: &mut [i64], a0: usize, b0: usize, limit0: usize) {
    let (mut a, mut b, mut limit) = (a0, b0, limit0);
    let mut balanced = true;
    let mut partitioned = true;
    loop {
        let length = b - a;
        if cfg!(bench_count) {
            N_PDQ.fetch_add(1, Relaxed);
        }
        if length <= 12 {
            insertion_sort(s, a, b);
            return;
        }
        if limit == 0 {
            heap_sort(s, a, b);
            return;
        }
        if !balanced {
            break_patterns(s, a, b);
            limit -= 1;
        }
        let (mut pivot, mut hint) = choose_pivot(s, a, b);
        if hint == 2 {
            reverse_range(s, a, b);
            pivot = (b - 1) - (pivot - a);
            hint = 1;
        }
        if balanced && partitioned && hint == 1 {
            if partial_insertion_sort(s, a, b) {
                return;
            }
        }
        if a > 0 && !(s[a - 1] < s[pivot]) {
            a = partition_equal(s, a, b, pivot);
            continue;
        }
        let (mid, already) = partition(s, a, b, pivot);
        partitioned = already;
        let left = mid - a;
        let right = b - mid;
        let threshold = length / 8;
        if left < right {
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

fn check(s: &[i64]) -> i64 {
    let mut sum: i64 = 0;
    for i in 0..s.len() {
        if i > 0 && s[i - 1] > s[i] {
            return -1;
        }
        sum += s[i] * (i % 1000) as i64;
    }
    sum
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        eprintln!("usage: sortints <n> <rounds> <seed>");
        std::process::exit(2);
    }
    let n: usize = a[1].parse().unwrap();
    let rounds: usize = a[2].parse().unwrap();
    let mut st: u64 = a[3].parse().unwrap();
    let mut xs: Vec<i64> = vec![0; n];
    let mut total: i64 = 0;
    for _ in 0..rounds {
        for p in 0..4 {
            for i in 0..n {
                if p == 0 {
                    st = st.wrapping_add(0x9e3779b97f4a7c15);
                    xs[i] = (mix(st) & 0xFFFFF) as i64;
                } else if p == 1 {
                    xs[i] = (n - i) as i64;
                } else if p == 2 {
                    xs[i] = (i % 1000) as i64;
                } else {
                    st = st.wrapping_add(0x9e3779b97f4a7c15);
                    xs[i] = (mix(st) >> 60) as i64;
                }
            }
            pdqsort(&mut xs, 0, n, bitlen(n));
            total += check(&xs);
        }
    }
    println!("{}", total);
    if cfg!(bench_count) {
        println!("counts pdq={} ins={} shift={} heap={} rev={} part={} parteq={} pis={} brk={}", N_PDQ.load(Relaxed), N_INS.load(Relaxed), N_SHIFT.load(Relaxed), N_HEAP.load(Relaxed), N_REV.load(Relaxed), N_PART.load(Relaxed), N_PARTEQ.load(Relaxed), N_PIS.load(Relaxed), N_BRK.load(Relaxed));
    }
}
