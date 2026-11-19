// verdict: ok
// rule: R9
// rule: R18
// note: every type of the kinds table with memory behind a private pointer is a `view`, `HeapList` among them, so an exported struct with a private `HeapList[int]` field says `linear view`
export linear view struct Bag {
    items: HeapList[int]
}
