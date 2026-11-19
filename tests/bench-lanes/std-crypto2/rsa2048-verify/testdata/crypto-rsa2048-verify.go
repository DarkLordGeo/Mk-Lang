// crypto-rsa2048-verify, Go: the standard library's RSA verification
// (rsa.VerifyPKCS1v15 and rsa.VerifyPSS with PSSSaltLengthEqualsHash) over
// the key, signatures and passes of mk/crypto-rsa2048-verify.mk's comment.
// Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
)

const modulus = "a2b451a07d0aa5f96e455671513550514a8a5b462ebef717094fa1fee82224e637f9746d3f7cafd31878d80325b6ef5a1700f65903b469429e89d6eac8845097b5ab393189db92512ed8a7711a1253facd20f79c15e8247f3d3e42e46e48c98e254a2fe9765313a03eff8f17e1a029397a1fa26a8dce26f490ed81299615d9814c22da610428e09c7d9658594266f5c021d0fceca08d945a12be82de4d1ece6b4c03145b5d3495d4ed5411eb878daf05fd7afc3e09ada0f1126422f590975a1969816f48698bcbba1b4d9cae79d460d8f9f85e7975005d9bc22c4e5ac0f7c1a45d12569a62807d3b9a02e5a530e773066f453d1f5b4c2e9cf7820283f742b9d5"
const sigPkcs1 = "840f5dac53106dd1f9c57219224cf51289290c42f20466875ba8e830ac5690e541536fcc8ab03b731f82bf66d83f194e7e180b3963ec7a2f3f7904a7ce49aed47da4d4b79421eaf937d301b3e696169297b797c32c076a12be4de0b58e003c5123051a84a10c62f8dac2f42a8640008eb3c7cccd6760ff5b51b689763922582845f048fb8150e5a7a6ca2eccc7bdc85349ad5b26c52137a79fa3fe5c29ab5cd7615013219c1941b6708e9c3c23feff5febaf0c8ebca5750b54e3e6e99a3e876b396f27860b7f3ec4e9191703c6332d944f6f69751167680c79c4f6b57f1cc8755d24b6ec158ccdbacdb23107a33cb6b332516c13274d1f9dccc21dced869e486"
const sigPss = "4f01e0c12b08625ecac89a69231906edf826380f37c959a96690d046316d68ffce9d5c471694fcebfc6b45534864689256e4fc81c78e583f675d0c94b449647451e81beff01a11a516d5e5ce3f1a910437cb8a3a5096b19fb15f4524a35b23d89cdba12cf5b71aac1047b28c562df7c5542c34ce23a182cf7e0e231934b17294799d44877a1d68ef1b8f073619b7618e6b7c22db20030d98cf591ffc3d4da5f58613ecd5ecfc3b40a1d02f40891ca43695cd4c088b05a8054c89c595a47e274816f35384226f74459ee63e25a1bfc03c360490552ec38343f8ace502f065303b00bc0ec320711b211fde92e57feb9013c3609342495ec0d7cabdec21e54acc38"

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: crypto-rsa2048-verify <keys> <reps> <seed>")
		os.Exit(2)
	}
	reps, _ := strconv.Atoi(os.Args[2])
	n, _ := new(big.Int).SetString(modulus, 16)
	key := &rsa.PublicKey{N: n, E: 65537}
	s1, _ := hex.DecodeString(sigPkcs1)
	s2, _ := hex.DecodeString(sigPss)
	digest := sha256.Sum256(nil)
	opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}
	var sum uint64
	for r := 0; r < reps; r++ {
		flip := byte(r & 1)
		digest[r%32] ^= flip
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], s1) == nil {
			sum++
		}
		if rsa.VerifyPSS(key, crypto.SHA256, digest[:], s2, opts) == nil {
			sum++
		}
		digest[r%32] ^= flip
	}
	fmt.Println(sum)
}
