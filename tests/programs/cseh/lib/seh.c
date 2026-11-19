/* The support pack's memcpy, memset and memcmp called from a package's C
   source (std.libc's __C_specific_handler lets it use __try) on a source or
   destination that faults, with the fault handled by the C code's own
   __except. Each victim keeps 10 values live across the call, so the register
   allocator puts some of them in the callee-saved rdi and rsi, and returns
   their sum plus 1000000 from the __except: 1016775 when the unwind
   restored every callee-saved register, 16775 with no fault. Cases 2 and 5
   check the unwind entries of memset and memcmp, which save rsi or rdi. */
typedef unsigned long long u64;
void *memcpy(void *, const void *, u64);
void *memset(void *, int, u64);
int memcmp(const void *, const void *, u64);

static char dst[8192];

__declspec(noinline) u64 victim_cpy(volatile u64 *in, const char *src, u64 n) {
    u64 a0 = in[0] * 3, a1 = in[1] * 5, a2 = in[2] * 7, a3 = in[3] * 11, a4 = in[4] * 13;
    u64 a5 = in[5] * 17, a6 = in[6] * 19, a7 = in[7] * 23, a8 = in[8] * 29, a9 = in[9] * 31;
    __try {
        memcpy(dst, src, n);
    } __except (1) {
        return 1000000 + a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9;
    }
    return a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9;
}

__declspec(noinline) u64 victim_set(volatile u64 *in, char *d, u64 n) {
    u64 a0 = in[0] * 3, a1 = in[1] * 5, a2 = in[2] * 7, a3 = in[3] * 11, a4 = in[4] * 13;
    u64 a5 = in[5] * 17, a6 = in[6] * 19, a7 = in[7] * 23, a8 = in[8] * 29, a9 = in[9] * 31;
    __try {
        memset(d, 0x5a, n);
    } __except (1) {
        return 1000000 + a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9;
    }
    return a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9;
}

/* The support pack's memcmp saves rsi on Win64; src faults. */
__declspec(noinline) u64 victim_cmp(volatile u64 *in, const char *d, const char *src, u64 n) {
    u64 a0 = in[0] * 3, a1 = in[1] * 5, a2 = in[2] * 7, a3 = in[3] * 11, a4 = in[4] * 13;
    u64 a5 = in[5] * 17, a6 = in[6] * 19, a7 = in[7] * 23, a8 = in[8] * 29, a9 = in[9] * 31;
    int r = 0;
    __try {
        r = memcmp(d, src, n);
    } __except (1) {
        return 1000000 + a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9;
    }
    return a0 + a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9 + (u64)(r != 0);
}

static void fill(volatile u64 *in) {
    for (int i = 0; i < 10; i++) in[i] = 100 + i;
}

/* Value checks of memcpy and memset at 2047 to 4109 bytes, within C's
   contract: memcpy(p, p, n), equal ranges, which LLVM's memcpy allows, and
   memset with a value above 255, which C converts to unsigned char.
   Returns the number of wrong bytes. The program runs on x86_64-windows
   only, so these reach the general-register code; the value checks of
   System V's rep path from 2048 bytes are TestErmsRun's
   x86_64-linux leaf. */
static unsigned char ovl[16384], ref[16384];

static void seed(void) {
    for (int i = 0; i < 16384; i++) ovl[i] = ref[i] = (unsigned char)(i * 131 + 7);
}

static u64 diff(void) {
    u64 bad = 0;
    for (int i = 0; i < 16384; i++) bad += ovl[i] != ref[i];
    return bad;
}

__declspec(noinline) u64 ovl_check(volatile u64 *sizes) {
    u64 bad = 0;
    for (int s = 0; s < 5; s++) {
        u64 n = sizes[s];
        seed();
        memcpy(ovl + 100, ovl + 100, n);
        bad += diff();
        seed();
        memset(ovl + 3, 0x1ab, n);
        for (u64 i = 0; i < n; i++) ref[3 + i] = 0xab;
        bad += diff();
    }
    return bad;
}

/* which: 0 no fault; 1 memcpy of 4096 bytes from address 16; 2 memset of
   4096 bytes at address 16; 3 memcpy of 1024 bytes from address 16; 4 the
   value checks; 5 memcmp of 4096 bytes against address 16. */
u64 seh_run(int which) {
    volatile u64 in[10];
    fill(in);
    if (which == 4) {
        volatile u64 sizes[5] = {2047, 2048, 2049, 4096, 4109};
        return ovl_check(sizes);
    }
    switch (which) {
    case 0:
        return victim_cpy(in, dst + 4096, 64);
    case 1:
        return victim_cpy(in, (const char *)16, 4096);
    case 2:
        return victim_set(in, (char *)16, 4096);
    case 5:
        return victim_cmp(in, dst, (const char *)16, 4096);
    default:
        return victim_cpy(in, (const char *)16, 1024);
    }
}
