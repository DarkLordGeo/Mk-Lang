// Why: a rejection sample is compared before and after a checker change
// over the same programs, so a generator change that adds a shape to some
// profiles must leave the other profiles' programs as they were.
//
// What: TestSoundGen_StreamsKept and soundStreamDigest.
//
// How: the SHA-256 of each kept profile's first programs from the default
// base, the clause-restored twins included and every version line left
// out, against the digests GenVersion 14 gave (S89, GenVersion 15, is drawn
// only in full and fields). A version that changes a kept profile's text
// writes the new digests and names the profile in GenVersion's list.

package corpus

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// soundStreamDigest is the SHA-256 of programs 0 to n-1 of profile p from
// DefaultSoundBase and their twins, each without its first (version) line.
func soundStreamDigest(p Profile, n int) string {
	h := sha256.New()
	for i := range n {
		seed := ProgramSeed(DefaultSoundBase, i)
		for _, prog := range []SoundProgram{GenerateSound(p, seed), GenerateSoundTwin(p, seed)} {
			_, body, _ := strings.Cut(prog.Text, "\n")
			fmt.Fprintf(h, "%d\n%s", len(body), body)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// TestSoundGen_StreamsKept: every profile but full and fields generates the
// text GenVersion 14 did: 30,000 programs of reject7 (the rejection
// sample's profile; the 20,000-program sample of the default base takes
// 29,706) and 200 of each other.
func TestSoundGen_StreamsKept(t *testing.T) {
	want := map[Profile]string{
		ProfileCore:    "f57b1d0bd2b83b941e6dacd77fe676e8dfee51982689945427ffc987a82d7625",
		ProfileTasks:   "d3d6c5664a34db7a24a081eac5db35ea729f5c8f8608113440d313410cbb39f2",
		ProfileReject:  "8824fb15ddbe3fb936d6c96d0b698108fad4f40933170842c02ee35bed1c898b",
		ProfileOracle:  "b5eef2f90a82441c7a5142af33e6d150ef702934d5c93bcfa97d860e7c5feddc",
		ProfileLang:    "236c22f0841c3a12a5a456da728ced8fd499d6cf904ea11d515ae872b0668891",
		ProfileVec:     "014e96593f1907cac937b59b747ab6cc25db43feb5a15816388ee4987acf31cd",
		ProfileViews:   "d95c51654f6f7cdc00b57c5de3bfd4b8c79ca3aa03934cb2ae99693f0173403b",
		ProfileReject7: "6c94e8856ad1957aa56a1e380f6a8a6d8a20345aede054b6da1a0766f87ba6db",
		ProfileRejectF: "ec9965a41872b942bae57d098fb18e864f5b7339a9969d2c9327984711622181",
	}
	for p := range nProfiles {
		w, ok := want[p]
		if !ok {
			continue
		}
		n := 200
		if p == ProfileReject7 {
			n = 30000
		}
		got := soundStreamDigest(p, n)
		t.Logf("%s %d: %s", p, n, got)
		if got != w {
			t.Errorf("%s: the first %d programs differ from the pinned stream (digest %s, want %s): a change to this profile's text writes the new digest and names the profile in GenVersion's list", p, n, got, w)
		}
	}
}
