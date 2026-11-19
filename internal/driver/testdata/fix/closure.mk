Above(xs: mut []i32, limit: i32) {
    xs.Sort(fn(a: &i32, b: &i32): bool { return *a + limit < *b })
}
