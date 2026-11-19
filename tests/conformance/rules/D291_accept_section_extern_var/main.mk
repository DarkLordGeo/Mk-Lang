// verdict: ok
// rule: D-291
// note: section on a const, a var and a function, and extern var read and addressed in unsafe, in a module pkg.mk lists; a write goes through the address
import k

Run(): uint {
    return k.End() + uint(k.Flag())
}
