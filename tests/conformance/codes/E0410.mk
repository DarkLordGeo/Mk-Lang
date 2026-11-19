// verdict: reject E0410
// note: an exported struct whose private field makes it a carrier, with no view
export struct Name { // <- E0410
    text: str
}
