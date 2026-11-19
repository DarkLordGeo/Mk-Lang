test.key is the TEST release key of internal/release. Its private half is public by design: anyone can sign with it.
It signs only test releases served by httptest. A release build of mk (one
with a compiled-in release address) refuses its signature, tools/release
sign refuses to sign with it, and keys.go never lists it. Its id is
6a225d61b201b8e9 (release.TestKeyID).
