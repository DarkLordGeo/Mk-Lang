// verdict: ok
// case: H22
Two(list: mut List[&i32]) {
    a := list.Pop()
    b := list.Pop()
    Use(&a)
    Use(&b)
}
