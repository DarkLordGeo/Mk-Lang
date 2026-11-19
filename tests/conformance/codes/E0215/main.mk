// verdict: reject E0215
// note: a fetched package (here vendored by name, as vendor/ini/ with no index.txt) asks for unsafe code the root does not grant (doc/packages.md)
import ini.src

export Keys(): int {
    return src.Keys()
}
