# internal/git test fixtures

The fixtures are synthetic only: nothing is downloaded or recorded. The recorder and the live test stay in the code, gated by `MK_NET=1`, and are not run.

Each fixture set is a directory holding a server's reply bodies, byte for byte. `httptest` serves them under `/repo`, with the Content-Type a smart HTTP server sends:

| File | Reply to | Content-Type |
|---|---|---|
| `discovery.pkt` | `GET /repo/info/refs?service=git-upload-pack`, `Git-Protocol: version=2` | `application/x-git-upload-pack-advertisement` |
| `ls-refs.pkt` | `POST /repo/git-upload-pack`, `command=ls-refs` | `application/x-git-upload-pack-result` |
| `fetch.pkt` | `POST /repo/git-upload-pack`, `command=fetch` | `application/x-git-upload-pack-result` |
| `expect.txt` | the result: `tag`, `commit`, then one `file <mode> <sha256> <path>` line per file; or `error <reason>` | |
| `meta.txt` | recorded sets only: the URL, the tag, whether it is annotated, each reply's Content-Type, and when and with what it was recorded | |

## `synthetic/`

The Go helper in `synth_test.go` writes these sets, following gitprotocol-v2 and gitprotocol-http as closely as they allow.

| Set | What it holds |
|---|---|
| `ok-annotated` | Discovery with the `# service=git-upload-pack` line and its flush. An annotated tag fetched with `deepen 1`, with shallow-info before packfile and band 2 progress. Whole objects, offset deltas, a reference delta ahead of its base, a chain. The reply ends in `0000`. |
| `ok-end-0002` | The same, with the reply ending in `0002` (response-end). |
| `ok-lightweight` | Discovery without the service line, and a lightweight tag. |
| `link` | A tree holding a link (refused). |
| `submodule` | A tree holding a submodule (refused). |
| `case-clash` | Two names that differ only in case (git accepts them; fetch refuses them). |
| `sha256` | A SHA-256 server. |
| `protocol-v0` | A version 0 server. |
| `err-discovery` | An ERR line in discovery. |
| `err-ls-refs` | An ERR line in ls-refs. |
| `err-shallow-info` | An ERR line in the shallow-info section. |
| `err-packfile` | An ERR line in the packfile section. |
| `band3-packfile` | A band 3 error in the packfile section. |

`synthetic-filter/` holds the manifest-read sets for `FetchFile`:

| Set | What it holds |
|---|---|
| `filter` | A `filter blob:none` fetch, then the blob alone. |
| `blob-refused` | The blob want is refused with ERR, and the client falls back to one whole fetch. |
| `no-filter` | A server without the filter feature. |

`TestSyntheticFixturesCurrent` holds the files equal to the helper's output. After changing the helper, run:

```
go test ./internal/git -run TestSyntheticFixturesCurrent -update
```

Read the diff before committing it. The fetch replies hold zlib streams from Go's compress/zlib, and Go does not promise the same output across releases. So a Go upgrade may also need `-update`; the objects stay the same.

## `recorded/` (not used)

`TestGitRecord` (`MK_NET=1 MK_GIT_RECORD=1`) would record one tag of a public repository into `recorded/<name>/`. `TestGitReplayRecorded` would then replay it; it skips while the directory is empty. `TestGitLive` (`MK_NET=1`) would fetch the tag live. The default target is `v0.9.1` of `https://github.com/pkg/errors`.

If a second target is ever recorded, choose a small repository with annotated tags. The code's fallback, git's own repository, is large and its tree holds a link.

If a fetch fails, the recorder writes the replies it received to `$MK_GIT_RECORD_FAILED/failed-<name>/`, so the failure can be read without a second download.
