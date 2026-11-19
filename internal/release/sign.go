// Why: a release is trusted through one signature over its checksum file,
// so signing and verifying must be small and exact.
//
// What: Key and KeyID, GenerateKey, PublicOf, ParsePrivate, IsTestKey, Sign
// and Verify: Ed25519 keys and SHA256SUMS.sig.
//
// How: a signature file is the key's id and the base64 Ed25519 signature of
// the SHA256SUMS bytes on one line. A key's id is the first 16 hex digits of
// its SHA-256. Verify finds the key by id; a release build refuses the test
// key even when it is given.

package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Key is a public key mk trusts: its id and its 32 bytes in standard
// base64, as keys.go lists them.
type Key struct {
	ID     string // KeyID of the public key
	Public string // base64 of the ed25519.PublicKey
}

// KeyID is a public key's id: the first 16 hex digits of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// The private key file's first word, before the base64 of the 32-byte seed.
const privateMagic = "mk-release-key-v1"

// TestKeyID and TestKeyPublic are the test key's, whose private half lives
// in internal/release/testdata/test.key and is public by design. They are
// here only so that a release build can refuse it: Verify refuses its
// signature in a release build, tools/release sign never signs with it,
// and keys.go must not list it (TestTrustedKeys).
const (
	TestKeyID     = "6a225d61b201b8e9"
	TestKeyPublic = "BHv1R5fOb9c9w4dUbDTNhRnbEnGJjdaKxu3wUHL7pwM="
)

// Errors of Verify.
var (
	ErrNoKeys       = errors.New("this mk trusts no release key (internal/release/keys.go lists none)")
	ErrUnknownKey   = errors.New("signed by a key this mk does not trust")
	ErrBadSignature = errors.New("the signature does not match " + SumsFile)
	ErrTestKey      = errors.New("signed by the test key, which a release build refuses")
)

// GenerateKey makes a new key pair from crypto/rand and returns the
// private key file's text and the public key's keys.go entry.
func GenerateKey() (private []byte, pub Key, err error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, Key{}, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return []byte(privateMagic + " " + base64.StdEncoding.EncodeToString(seed) + "\n"), PublicOf(priv), nil
}

// PublicOf is the Key of a private key.
func PublicOf(priv ed25519.PrivateKey) Key {
	p := priv.Public().(ed25519.PublicKey)
	return Key{ID: KeyID(p), Public: base64.StdEncoding.EncodeToString(p)}
}

// ParsePrivate reads a private key file GenerateKey wrote.
func ParsePrivate(data []byte) (ed25519.PrivateKey, error) {
	magic, b64, ok := strings.Cut(strings.TrimSpace(string(data)), " ")
	if !ok || magic != privateMagic {
		return nil, fmt.Errorf("release: not an mk release key file (want %q and the base64 seed)", privateMagic)
	}
	seed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("release: the key file's seed is not 32 bytes of base64")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// IsTestKey reports whether a public key is the test key.
func IsTestKey(k Key) bool { return k.ID == TestKeyID || k.Public == TestKeyPublic }

// Sign is the SHA256SUMS.sig text for sums: "<key id> <base64 signature>\n".
func Sign(priv ed25519.PrivateKey, sums []byte) []byte {
	k := PublicOf(priv)
	return []byte(k.ID + " " + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n")
}

// Verify checks sig, a SHA256SUMS.sig, over sums against keys and returns
// the key that signed. The signature names its key by id; a key that is
// not in keys is ErrUnknownKey, an empty keys ErrNoKeys, a signature that
// does not verify (or a malformed or truncated file) ErrBadSignature, and
// in a release build the test key is ErrTestKey even when keys holds it.
func Verify(sums, sig []byte, keys []Key) (Key, error) {
	if len(keys) == 0 {
		return Key{}, ErrNoKeys
	}
	text := string(sig)
	if !strings.HasSuffix(text, "\n") {
		return Key{}, fmt.Errorf("%w (%s does not end in a newline)", ErrBadSignature, SigFile)
	}
	id, b64, ok := strings.Cut(strings.TrimSuffix(text, "\n"), " ")
	if !ok {
		return Key{}, fmt.Errorf("%w (%s is not \"<key id> <signature>\")", ErrBadSignature, SigFile)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return Key{}, fmt.Errorf("%w (the signature is not %d bytes of base64)", ErrBadSignature, ed25519.SignatureSize)
	}
	for _, k := range keys {
		if k.ID != id {
			continue
		}
		if IsTestKey(k) && ReleaseBuild() {
			return Key{}, ErrTestKey
		}
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		if err != nil || len(pub) != ed25519.PublicKeySize || KeyID(pub) != k.ID {
			return Key{}, fmt.Errorf("release: trusted key %s is malformed", k.ID)
		}
		if !ed25519.Verify(pub, sums, raw) {
			return Key{}, ErrBadSignature
		}
		return k, nil
	}
	if id == TestKeyID && ReleaseBuild() {
		return Key{}, ErrTestKey
	}
	return Key{}, fmt.Errorf("%w (key %s)", ErrUnknownKey, id)
}
