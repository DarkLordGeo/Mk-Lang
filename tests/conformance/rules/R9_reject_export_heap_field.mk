// verdict: reject E0410
// rule: R9
// rule: R18
// note: the twin of R9_accept_export_heap_field: without `view` the exported struct does not state the kind its private `HeapList[int]` field gives it, E0410 at the name
export linear struct Bag { // <- E0410
    items: HeapList[int]
}
