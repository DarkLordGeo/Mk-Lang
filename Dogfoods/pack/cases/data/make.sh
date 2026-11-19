#!/bin/sh
# Why: pack's cases need archives that are whole and archives damaged at
# exact bytes, and those must be rebuilt by a script, not by hand.
#
# What: rebuilds the fixtures of the cases: the tree and dups directories,
# the key files, the archives (plain, gzip, signed, sealed) and their
# damaged and cut copies. Run from this directory with a built pack:
#   sh make.sh /path/to/pack.exe
#
# How: put() writes one byte at an offset into a copy, at offsets read from
# the archive's own header. The plain archives come out the same on every
# machine; the sealed ones do not (their ephemeral key is random), so they
# are made once and their damaged copies come from the committed ones.
set -e
P="$1"

rm -rf tree
mkdir -p tree/docs/notes tree/src
printf 'pack test tree\n' > tree/README.txt
printf 'How to use pack.\n\nCreate, list, verify, extract.\n' > tree/docs/guide.txt
printf 'write more cases\n' > tree/docs/notes/todo.txt
i=1
while [ $i -le 4000 ]; do
    echo "line $i of the big file, which spans more than one sealed chunk"
    i=$((i + 1))
done > tree/src/big.txt
# 96 KiB that DEFLATE barely shrinks, so a sealed archive has two chunks
awk 'BEGIN { x = 1; for (i = 0; i < 98304; i++) { x = (x * 25173 + 13849) % 65536; printf "%c", int(x / 256) } }' > tree/src/noise.bin

# a tree in which some files repeat another's contents, for list --dups
rm -rf dups
mkdir -p dups/copy dups/empty
printf 'the same text\n' > dups/a.txt
printf 'the same text\n' > dups/copy/a.txt
printf 'the same text\n' > dups/copy/b.txt
printf 'only once\n' > dups/once.txt
: > dups/empty/one
: > dups/empty/two

# alice's and bob's key files are version 1 (hex), written before version 2
# (base64); they are kept, so the cases still read a version 1 file
[ -f alice.key ] || "$P" keygen --seed 0101010101010101010101010101010101010101010101010101010101010101 alice
[ -f bob.key ] || "$P" keygen --seed 0202020202020202020202020202020202020202020202020202020202020202 bob
"$P" keygen --seed 0404040404040404040404040404040404040404040404040404040404040404 dave

"$P" create --out plain.pk --sign alice.key tree
"$P" create --out gzip.pk --gzip tree
"$P" create --out dups.pk dups
[ -f sealed.pk ] || "$P" create --out sealed.pk --sign alice.key --to bob.pub tree

# put BYTE (decimal) at OFFSET of a copy of SRC named DST
put() {
    cp "$1" "$2"
    printf "$(printf '\\%03o' "$4")" | dd of="$2" bs=1 seek="$3" conv=notrunc 2>/dev/null
}
# the manifest's length is at offset 8, little-endian
mlen=$(od -An -tu4 -j8 -N4 plain.pk | tr -d ' ')
sig=$((12 + mlen))
data=$((sig + 96))
size=$(wc -c < plain.pk)

put plain.pk bad-magic.pk 0 88
put plain.pk bad-version.pk 4 9
put plain.pk bad-header.pk 6 1
put plain.pk bad-manifest.pk 12 113
# a digit of the first file's digest in the unsigned archive
first=$(grep -abo 'file [0-9]* ' gzip.pk | head -1 | cut -d: -f1)
len=$(grep -ao 'file [0-9]* ' gzip.pk | head -1 | wc -c)
put gzip.pk bad-digest.pk $((first + len - 1)) 102
put plain.pk bad-signature.pk $((sig + 40)) 0
put plain.pk bad-signer.pk $((sig + 5)) 0
put plain.pk bad-data.pk $((data + 100)) 255
head -c $((data + 1000)) plain.pk > cut-data.pk
head -c 40 plain.pk > cut-manifest.pk
head -c 8 plain.pk > cut-header.pk
# README.txt sorts first; the same length keeps the manifest's size
sed 's|README.txt|../ADME.tx|' gzip.pk > unsafe.pk

put sealed.pk sealed-bad-header.pk 20 0
put sealed.pk sealed-bad-chunk.pk 200 0
ssize=$(wc -c < sealed.pk)
put sealed.pk sealed-bad-last.pk $((ssize - 1)) 0
head -c $((ssize - 10)) sealed.pk > sealed-cut.pk

# sealed archives of version 2 (HKDF keys), one per cipher, to dave's
# version 2 key; random like sealed.pk, so kept once made
[ -f sealed-aes.pk ] || "$P" create --out sealed-aes.pk --sign alice.key --to dave.pub --cipher aes-256-gcm tree
[ -f sealed-chacha.pk ] || "$P" create --out sealed-chacha.pk --to dave.pub --cipher chacha20-poly1305 tree
# a byte of the first AES-256-GCM chunk, the version and the cipher changed
put sealed-aes.pk sealed-aes-bad-chunk.pk 300 $(( ($(od -An -tu1 -j300 -N1 sealed-aes.pk) + 1) % 256 ))
put sealed-aes.pk sealed-bad-version.pk 4 9
put sealed-aes.pk sealed-bad-cipher.pk 5 7
# key files that do not read: a base64 digit out of the alphabet, and one
# past the length a key file can have
printf 'pack-public-key 2\nsign AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA*=\nbox AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n' > bad-base64.pub
{ cat dave.pub; i=0; while [ $i -lt 40 ]; do echo '# padding past the longest key file'; i=$((i + 1)); done; } > long.pub
