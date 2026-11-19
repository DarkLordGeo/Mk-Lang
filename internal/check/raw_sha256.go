// Why: std.raw's SHA-256 intrinsics (std/raw/sha256.mk) need only their cpu
// feature, so they should be callable outside unsafe.
//
// What: an init that marks Sha256Rounds4 and Sha256Schedule4 safe (rawSafe)
// and as running in SSE registers (rawSSE).
//
// How: the cpu call rule (E1101 where the context lacks the feature) guards
// each call; on x86_64 each also needs sse2 in the context (raw_sse.go).

package check

func init() {
	for _, n := range []string{"Sha256Rounds4", "Sha256Schedule4"} {
		rawSafe[n] = true
		rawSSE[n] = true // E1106 needs sse2 on x86_64 (raw_sse.go)
	}
}
