// verdict: reject E1216
Spin() {
    loop { // <- E1216
        break
    }
}
