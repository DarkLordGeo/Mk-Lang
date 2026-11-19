// verdict: reject E0412
struct Secret {
    n: int
}

export Make(): Secret { // <- E0412
    return Secret(n: 1)
}
