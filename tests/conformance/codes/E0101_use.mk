// verdict: reject E0101
// note: foreign form, `use a::b` ("write `import a.b`"). The foreign form consumes its line, so its `::` draws no E1213
use std::fs // <- E0101

Zero(): int {
    return 0
}
