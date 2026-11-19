// verdict: reject E0208
// note: an import binding never used in its file; anchored at the import line
import util // <- E0208

Ten(): int {
    return 10
}
