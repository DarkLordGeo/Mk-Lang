// verdict: reject E0705
// note: a const's fuel is an integer literal or a named const; a float or a string literal is neither
const A = 1 fuel 1.5 // <- E0705

const B = 2 fuel "x" // <- E0705
