// Why: `mk profile merge` combines the profiles of several runs.
// What: BenchmarkPGOMerge.
// How: merges four generated profiles of 10,000 functions each and reports
// records merged per second.

package pgo

import (
	"fmt"
	"testing"
)

// benchProfile is n records of 8 counters each, keyed like instance symbols.
func benchProfile(n int, seed uint64) *Profile {
	p := &Profile{Records: make([]Record, n)}
	for i := range p.Records {
		counts := make([]uint64, 8)
		for j := range counts {
			counts[j] = seed*uint64(i+1) + uint64(j)
		}
		p.Records[i] = Record{Key: fmt.Sprintf("mk.app.mod%03d.F%06d", i%97, i), Checksum: sum(byte(i)), Counts: counts}
	}
	m, err := Merge(p) // sorts into canonical order
	if err != nil {
		panic(err)
	}
	return m
}

// BenchmarkPGOMerge merges four profiles of 10,000 functions each, as
// `mk profile merge` does after four runs of one program; it reports records
// merged per second.
func BenchmarkPGOMerge(b *testing.B) {
	const n = 10000
	ps := []*Profile{benchProfile(n, 1), benchProfile(n, 2), benchProfile(n, 3), benchProfile(n, 4)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Merge(ps...); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(4*n)*float64(b.N)/b.Elapsed().Seconds(), "records/s")
}
