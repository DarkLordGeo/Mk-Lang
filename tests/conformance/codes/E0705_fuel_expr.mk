// verdict: reject E0705
// note: a const's fuel is an integer literal or a named const, as a gen line's is
const Limit = 1000

const Total = 1 fuel Limit * 2 // <- E0705
