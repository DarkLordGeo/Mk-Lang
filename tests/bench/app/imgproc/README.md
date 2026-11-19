# imgproc (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the imgproc dogfood, `Dogfoods/imgproc`, unchanged. The row has no twin, so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk imgproc`).

**Task.** The dogfood's bench case (`cases/bench.args`) at a larger size: `imgproc info synth:WxH blur sharpen equalize sobel resize=W2x0 gamma=2.2 swap=bgr posterize=5 gray dilate threshold`. imgproc makes its colour test pattern in memory, runs the pipeline in tiles and prints the result's statistics. No file is read or written.

**Labelled.** imgproc writes its own vector code on `std.simd`'s `Vec` and chooses its kernel path at run time: `--path auto`, the default, takes the widest path the CPU has (scalar, v128, w256, w512, v256 or v512), through the tables of its unsafe module `src/dispatch`. The app table's rules keep such a dogfood out of a twinned row unchanged; an Mk-only row may keep it, as here. So the path timed is the machine's own, and two mk binaries compare on one machine only.

**Check.** The SHA-256 of the standard output (`digest`).

**Environment.** `MK_CARRIERS=1`.

**Sizes.** The quick arguments are the dogfood's bench case (`synth:2048x1536`, `resize=1024x0`); the publish size is provisional, and `verify -publish-size` fixes it in the quiet window.
