# libc-search (std.libc)

std.libc's `strlen` and `memchr` against the C runtime's, under one C kernel
(`mk/kernel.c`), for the audit rows of std.libc's string functions.

```
b = n bytes from splitmix64(seed 1), each 1 + (x mod 254); b[n] = 0
sum = 0
for r in 0..reps:
    z = n - 1 - r mod 64; f = n - 2 - r mod 64
    save b[z], b[f]; b[z] = 0; b[f] = 255
    sum += strlen(b)                 -- z
    sum += memchr(b, 255, n) - b     -- f: no other byte is 255
    restore b[z], b[f]
print sum
```

So sum = the sum over r of (2n - 3 - 2 (r mod 64)): 419,417,648 at n = 1,048,576 and
200 passes, 20,970,860,768 at 10,000 passes.

The Mk column is a Mk `Main` that calls the kernel, compiled as the package's C source and
linked against std.libc; the C column includes the same kernel and links the C runtime.
