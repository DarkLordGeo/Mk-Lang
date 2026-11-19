# textkit (app, Tier A)

**An Mk-only row** (tests/bench/README.md, "Tier A"). The Mk side is the textkit dogfood, `Dogfoods/textkit`, unchanged, built as its bin `textkit` (the package also has `ucdcases`). The row has no twin (the other languages' std has no UAX 29 segmentation or normalization), so it has no peer, no ratio to another language and no defect line: it is compared across mk binaries (`tools/bench compare --base <mk A> --head <mk B> -cols mk textkit-grep textkit-wc`).

**Task.** Two variants over one generated text file:
- `grep`: `textkit grep --count --ignore-case '\b[aeiou][a-z]{3}\b' {data}`, the command of the dogfood's bench case, with a pattern the input matches: the count of lines holding a four-letter word that starts with a vowel, case-folded.
- `wc`: `textkit wc --lines --words --chars --bytes --clusters --width {data}`: lines, words, characters, bytes and grapheme clusters by UAX 29, and the widest line in terminal columns.

**Check.** The SHA-256 of the standard output (`digest`), with the data path written back as `$DATA`.

**Input.** `tools/bench gen` kind `words`, seed 8: ASCII words, spaces and line ends, so textkit reads its Unicode tables but no multi-byte path runs.

**Seen, not changed.** When a count has 8 digits or more, `wc` prints the columns with no space between them (`4258352320000033200000332000003` for the words, characters, bytes and clusters of a 32,000,000-byte file of this kind and seed; at 1,000,000 bytes the columns are apart), so its output is ambiguous to a reader. It is still the same for a given input, so the row's checksum holds. A defect of the dogfood; the row uses the dogfood unchanged.

**Environment.** `MK_CARRIERS=1`.

**Sizes.** One data spec, so quick and publish arguments are the same; the size is provisional and `verify -publish-size` fixes it in the quiet window.
