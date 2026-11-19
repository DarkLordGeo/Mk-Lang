# pack (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the pack dogfood, `Dogfoods/pack`, unchanged. The row has no twin, so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk pack`).

**Task.** The dogfood's bench case (`cases/bench.args`) on a larger tree:

    pack create --sign {pkg}/cases/data/alice.key --to {pkg}/cases/data/bob.pub --out {out} {data}

pack walks the tree, compresses each file with DEFLATE, writes a BLAKE3 manifest, signs it with Ed25519 (alice.key) and seals the archive to bob.pub with X25519 and ChaCha20-Poly1305, writing it by fsync and rename. The keys are the dogfood's own, named through `{pkg}`, the package's directory.

**Check.** A sealed archive's bytes differ from one run to the next (a fresh ephemeral key and nonce for each seal: two runs on the same tree wrote archives with different SHA-256s), so the output check is `then`: after each checked run, `pack sum --key {pkg}/cases/data/bob.key {out}` opens the archive with the recipient's key and prints the SHA-256 of every file it holds, which the tree fixes. The checksum is the SHA-256 of that output, with the paths written back as `$DATA`, `$OUT` and `$PKG`.

**Input.** `tools/bench gen` kind `tree`, seed 7: nested directories of source-like text, with hidden names, binary files and CR LF files. pack packs hidden files too.

**Environment.** `MK_CARRIERS=1`: one carrier, as every single-threaded app row.

**Sizes.** One data spec, so quick and publish arguments are the same; the size is provisional and `verify -publish-size` fixes it in the quiet window.
