# tracer (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the tracer dogfood, `Dogfoods/tracer`, unchanged. The row has no twin (another language's libm differs from Mk's math in the last bits of `sin` and `exp`, so its image bytes would differ), so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk tracer`).

**Task.** The dogfood's bench case (`cases/bench.args`) at a larger image: `tracer render {pkg}/cases/data/mesh.scene {out} --size WxH --samples 8`. tracer reads the scene (the dogfood's own, named through `{pkg}`), traces it in bands of rows and writes a binary PPM image to `{out}`.

**Check.** The output check is `digest`: the checksum is the SHA-256 of the standard output (empty) and of the image file.

**Environment.** `MK_CARRIERS=1`, so the band tasks run on one carrier.

**Sizes.** The quick arguments are the dogfood's bench case (160x120, 8 samples); the publish size is provisional, and `verify -publish-size` fixes it in the quiet window.
