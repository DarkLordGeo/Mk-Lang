// verdict: ok
// case: H02
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

Tail(head: mut Node) {
    var cur := head
    for {
        next := cur.Next()
        if n := next {
            cur = n
        } else {
            break
        }
    }
    cur.value = 1
}
