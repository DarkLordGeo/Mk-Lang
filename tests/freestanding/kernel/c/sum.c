/* A C source of the kernel: a byte sum with a 64-byte local, a leaf that
   would use the red zone without -mno-red-zone. */
unsigned long SumBytes(const unsigned char *p, unsigned long n) {
    unsigned char buf[64];
    unsigned long s = 0;
    for (unsigned long i = 0; i < n; i++) {
        buf[i & 63] = p[i];
        s += buf[i & 63];
    }
    return s;
}
