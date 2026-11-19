// verdict: reject E1204
// needs: parse
// note: the parser's form; the checker's .await form is E1204_await.mk
async Fetch(): int { // <- E1204
    return 1
}
