// verdict: reject E0601
// note: the `&str` result could come from `self` (a `Table` holds `str`) or from `k`, so the types do not settle it; the rewrite is `from self`
struct Table {
    name: str
}

Table.Get(&self, k: &str): &str { // <- E0601
    if k.Len() > 0 {
        return k
    }
    return &self.name
}
