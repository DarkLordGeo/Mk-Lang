// Why: HMAC and HKDF feed TLS 1.3's key schedule, so they must match Go and
// the published vectors exactly.
// What: Hmac256/384/512 and HKDF against crypto/hmac and crypto/hkdf, RFC 5869,
// and the Wycheproof HMAC, HKDF and AES-GCM vectors.
// How: through the AEAD driver, on its four variants.

package std

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestSTDCRYPTO_HmacMatchesGo: HMAC-SHA256, -SHA384 and -SHA512 over
// keys around each block size (64 and 128 bytes, where the key is hashed
// first) and data around the block boundaries.
func TestSTDCRYPTO_HmacMatchesGo(t *testing.T) {
	t.Parallel()
	keyLens := []int{0, 1, 31, 32, 33, 47, 48, 49, 63, 64, 65, 127, 128, 129, 200, 300}
	dataLens := []int{0, 1, 55, 56, 63, 64, 65, 111, 112, 119, 120, 127, 128, 129, 239, 240, 256, 1000}
	var lines []string
	for _, bits := range []int{256, 384, 512} {
		for i, kl := range keyLens {
			for j, dl := range dataLens {
				seed := uint64(bits*10000 + i*100 + j)
				lines = append(lines, fmt.Sprintf("M %d %s %s", bits, cr2X(cr2Rand(seed, kl)), cr2X(cr2Rand(seed+7, dl))))
			}
		}
	}
	cr2Check(t, "Hmac", lines)
}

// TestSTDCRYPTO_HkdfMatchesGo: Extract and Expand for each hash, empty
// and long salts, info, and output lengths at 0, around one block, and at
// and one past 255 blocks (BadLength, nothing written).
func TestSTDCRYPTO_HkdfMatchesGo(t *testing.T) {
	t.Parallel()
	var lines []string
	for _, bits := range []int{256, 384, 512} {
		hl := bits / 8
		outs := []int{0, 1, hl - 1, hl, hl + 1, 2*hl + 3, 100, 255 * hl, 255*hl + 1, 10000}
		for i, sl := range []int{0, 1, 32, 64, 129, 200} {
			for j, il := range []int{0, 1, 22, 80} {
				for k, n := range outs {
					seed := uint64(bits*1000 + i*100 + j*10 + k)
					lines = append(lines, fmt.Sprintf("K %d %s %s %s %d", bits,
						cr2X(cr2Rand(seed, sl)), cr2X(cr2Rand(seed+1, 13+k)), cr2X(cr2Rand(seed+2, il)), n))
				}
			}
		}
	}
	cr2Check(t, "Hkdf", lines)
}

// TestSTDCRYPTO_HkdfRfc5869: RFC 5869 appendix A.1 to A.3 (SHA-256), the
// bytes written here.
func TestSTDCRYPTO_HkdfRfc5869(t *testing.T) {
	t.Parallel()
	lines := []string{
		"K 256 000102030405060708090a0b0c " + "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b" + " f0f1f2f3f4f5f6f7f8f9 42",
		"K 256 " + cr2Seq(0x60, 80) + " " + cr2Seq(0x00, 80) + " " + cr2Seq(0xb0, 80) + " 82",
		"K 256  0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b  42",
	}
	want := []string{
		"K 3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
		"K b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87",
		"K 8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8",
	}
	for _, v := range cr2Variants {
		got := cr2Ask(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s: case A.%d\n got  %s\n want %s", v.name, i+1, got[i], want[i])
			}
		}
	}
}

// cr2Seq is n bytes from start counting up, as hex.
func cr2Seq(start byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return hex.EncodeToString(b)
}

// cr2Wycheproof reads Mk-evidence's wycheproof/testvectors_v1/name, or
// skips the test with the reason when the evidence is absent.
func cr2Wycheproof(t *testing.T, name string, v any) {
	t.Helper()
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	b, err := os.ReadFile(filepath.Join(root, "wycheproof", "testvectors_v1", name))
	if err != nil {
		t.Skipf("Wycheproof vectors not found in Mk-evidence/wycheproof (set MK_EVIDENCE): %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type cr2WpMac struct {
	TestGroups []struct {
		KeySize int `json:"keySize"`
		TagSize int `json:"tagSize"`
		Tests   []struct {
			TcID   int    `json:"tcId"`
			Key    string `json:"key"`
			Msg    string `json:"msg"`
			Tag    string `json:"tag"`
			Result string `json:"result"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// TestSTDCRYPTO_WycheproofHmac: every vector of hmac_sha256,
// hmac_sha384 and hmac_sha512. A vector with a truncated tag is judged by the prefix of
// the full tag, which is how a verifier of a truncated tag works, so every
// vector runs: 100 percent of the three files.
func TestSTDCRYPTO_WycheproofHmac(t *testing.T) {
	t.Parallel()
	for _, f := range []struct {
		name string
		bits int
	}{{"hmac_sha256_test.json", 256}, {"hmac_sha384_test.json", 384}, {"hmac_sha512_test.json", 512}} {
		var wp cr2WpMac
		cr2Wycheproof(t, f.name, &wp)
		var lines []string
		type exp struct {
			tag   string
			valid bool
			id    int
		}
		var exps []exp
		for _, g := range wp.TestGroups {
			for _, c := range g.Tests {
				lines = append(lines, fmt.Sprintf("M %d %s %s", f.bits, c.Key, c.Msg))
				exps = append(exps, exp{c.Tag, c.Result == "valid", c.TcID})
			}
		}
		for _, v := range cr2Variants {
			got := cr2Ask(t, v, lines)
			for i, e := range exps {
				mac := got[i][2:]
				ok := len(e.tag) <= len(mac) && mac[:len(e.tag)] == e.tag
				if ok != e.valid {
					t.Errorf("%s %s tcId %d: tag match %v, Wycheproof says valid %v", f.name, v.name, e.id, ok, e.valid)
				}
			}
		}
		t.Logf("%s: %d of %d vectors run on %d variants", f.name, len(lines), len(lines), len(cr2Variants))
	}
}

type cr2WpHkdf struct {
	TestGroups []struct {
		Tests []struct {
			TcID   int    `json:"tcId"`
			Ikm    string `json:"ikm"`
			Salt   string `json:"salt"`
			Info   string `json:"info"`
			Size   int    `json:"size"`
			Okm    string `json:"okm"`
			Result string `json:"result"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// TestSTDCRYPTO_WycheproofHkdf: every vector of hkdf_sha256,
// hkdf_sha384 and hkdf_sha512: a valid
// vector's okm, and an invalid one (a size past 255 blocks) refused with
// BadLength and nothing written. 100 percent of the three files.
func TestSTDCRYPTO_WycheproofHkdf(t *testing.T) {
	t.Parallel()
	for _, f := range []struct {
		name string
		bits int
	}{{"hkdf_sha256_test.json", 256}, {"hkdf_sha384_test.json", 384}, {"hkdf_sha512_test.json", 512}} {
		var wp cr2WpHkdf
		cr2Wycheproof(t, f.name, &wp)
		var lines, want []string
		var ids []int
		for _, g := range wp.TestGroups {
			for _, c := range g.Tests {
				lines = append(lines, fmt.Sprintf("K %d %s %s %s %d", f.bits, c.Salt, c.Ikm, c.Info, c.Size))
				ids = append(ids, c.TcID)
				switch c.Result {
				case "valid":
					want = append(want, "K "+c.Okm)
				default:
					want = append(want, "K bad-length true")
				}
			}
		}
		for _, v := range cr2Variants {
			got := cr2Ask(t, v, lines)
			for i := range lines {
				if got[i] != want[i] {
					t.Errorf("%s %s tcId %d:\n got  %s\n want %s", f.name, v.name, ids[i], cryptoTail(got[i], 200), cryptoTail(want[i], 200))
				}
			}
		}
		t.Logf("%s: %d of %d vectors run on %d variants", f.name, len(lines), len(lines), len(cr2Variants))
	}
}

type cr2WpAead struct {
	TestGroups []struct {
		IvSize  int `json:"ivSize"`
		KeySize int `json:"keySize"`
		TagSize int `json:"tagSize"`
		Tests   []struct {
			TcID   int    `json:"tcId"`
			Key    string `json:"key"`
			Iv     string `json:"iv"`
			Aad    string `json:"aad"`
			Msg    string `json:"msg"`
			Ct     string `json:"ct"`
			Tag    string `json:"tag"`
			Result string `json:"result"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// TestSTDCRYPTO_WycheproofAesGcm: aes_gcm_test.json. Filtered by name,
// since AesGcm's surface cannot express them: IVs other than 96 bits (the
// nonce is &[12]u8) and tags other than 128 bits (Seal writes 16). A
// 192-bit key is run and must be refused with BadLength (AesGcm takes
// AES-128 and AES-256). Every other vector runs: a valid one sealed
// to ct || tag and opened to msg, an invalid one refused by Open with
// BadTag and out unchanged. The counts are logged.
func TestSTDCRYPTO_WycheproofAesGcm(t *testing.T) {
	t.Parallel()
	var wp cr2WpAead
	cr2Wycheproof(t, "aes_gcm_test.json", &wp)
	var lines, want []string
	var ids []int
	total, skipIv, skipTag, refused := 0, 0, 0, 0
	for _, g := range wp.TestGroups {
		for _, c := range g.Tests {
			total++
			switch {
			case g.IvSize != 96:
				skipIv++
				continue
			case g.TagSize != 128:
				skipTag++
				continue
			case g.KeySize == 192:
				refused++
				lines = append(lines, fmt.Sprintf("O %s %s %s %s", c.Key, c.Iv, c.Aad, c.Ct+c.Tag))
				want = append(want, "O bad-length true")
				ids = append(ids, c.TcID)
				continue
			}
			if c.Result == "valid" {
				lines = append(lines, fmt.Sprintf("G %s %s %s %s", c.Key, c.Iv, c.Aad, c.Msg))
				want = append(want, "G "+c.Ct+c.Tag+" true")
				ids = append(ids, c.TcID)
				lines = append(lines, fmt.Sprintf("O %s %s %s %s", c.Key, c.Iv, c.Aad, c.Ct+c.Tag))
				want = append(want, "O ok "+c.Msg+" true")
				ids = append(ids, c.TcID)
			} else {
				lines = append(lines, fmt.Sprintf("O %s %s %s %s", c.Key, c.Iv, c.Aad, c.Ct+c.Tag))
				want = append(want, "O bad-tag true")
				ids = append(ids, c.TcID)
			}
		}
	}
	for _, v := range cr2Variants {
		got := cr2Ask(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s tcId %d:\n got  %s\n want %s", v.name, ids[i], cryptoTail(got[i], 200), cryptoTail(want[i], 200))
			}
		}
	}
	t.Logf("aes_gcm_test.json: %d vectors; %d run (%d of them 192-bit keys, refused as AesGcm must), %d skipped for an IV other than 96 bits, %d for a tag other than 128 bits", total, total-skipIv-skipTag, refused, skipIv, skipTag)
}
