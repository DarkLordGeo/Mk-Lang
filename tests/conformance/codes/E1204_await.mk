// verdict: reject E1204
// needs: types
// note: .await is the checker's form, judged in the types group
Wait(n: int): int {
    return n.await // <- E1204
}
