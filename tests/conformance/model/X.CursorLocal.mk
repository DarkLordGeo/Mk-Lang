// verdict: ok
// case: X.CursorLocal
// note: the field row kills the loan
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

struct C {
    cur: mut Node
}

Walk(head: mut Node) {
    var c := C(cur: head)
    for {
        next := c.cur.Next()
        if n := next {
            c.cur = n
        } else {
            return
        }
    }
}
