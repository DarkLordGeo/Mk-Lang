/* CSR-extern-e1's F: F(h: mut H,
   x: S into h) stores x into h, then writes x.b through h->s.a, which
   exceeds its signature on purpose. Mk passes `mut H` as a pointer. S stays
   by value: the witness needs x to be a by-value S whose own loans are p's
   and n's, as in sr2 (a `&S` would store the loan of a temporary instead).
   Both sides use the platform C ABI for the 16-byte S. */
typedef struct { const int **a; const int *b; } S;
typedef struct { S s; } H;

void F(H *h, S x) {
    h->s = x;
    *h->s.a = x.b;
}
