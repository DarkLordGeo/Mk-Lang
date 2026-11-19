// verdict: ok
// case: R15.Last
struct Node {
    value: i32
    link: ?mut Node
}

Node.Next(mut self): ?mut Node {
    if n := mut self.link {
        return some(mut **n)
    }
    return none
}

Last(head: mut Node): mut Node {
    var cur := head
    for {
        next := cur.Next()
        if n := next {
            cur = n
        } else {
            return cur
        }
    }
}
