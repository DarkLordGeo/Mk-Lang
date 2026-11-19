// Why: std.raw's SHA-1 intrinsics (std/raw/sha1.mk) need only their cpu
// feature, so they should be callable outside unsafe, as the SHA-256 ones are.
//
// What: an init that marks Sha1Rounds4, Sha1NextE and Sha1Schedule4 safe
// (rawSafe) and as running in SSE registers (rawSSE).
//
// How: the cpu call rule (E1101 where the context lacks the feature) guards
// each call; on x86_64 each also needs sse2 (raw_sse.go). Sha1Rounds4's round
// group is a const parameter, so its requires clause is judged at the call
// (E0706).

package check

func init() {
	for _, n := range []string{"Sha1Rounds4", "Sha1NextE", "Sha1Schedule4"} {
		rawSafe[n] = true
		rawSSE[n] = true // E1106 needs sse2 on x86_64 (raw_sse.go)
	}
}
