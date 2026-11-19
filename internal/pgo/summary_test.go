// Why: Summary decides which functions are hot, and its answer must not
// depend on which other packs a build happens to compile.
// What: TestSummaryIsWholeProfile and TestHotTies.
// How: fixed profiles with stale records and ties at the hot threshold.

package pgo

import "testing"

// TestSummaryIsWholeProfile: Summarize runs over every record of the
// profile, stale ones included, because only the profile's hash is in a pack
// key. Summarizing only the matched functions would make G hot in the first
// case; it is not done because the matched set depends on other packs'
// checksums, which no pack key covers (a warm build must equal a cold one).
func TestSummaryIsWholeProfile(t *testing.T) {
	t.Parallel()
	// F's live checksum is sum(3): its record at sum(1) is stale.
	p := &Profile{Records: []Record{
		{Key: "F", Checksum: sum(1), Counts: []uint64{1e9}},
		{Key: "G", Checksum: sum(2), Counts: []uint64{10}},
	}}
	if err := p.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if _, ok := p.Lookup("G", sum(2), 1); !ok {
		t.Fatal("G@2 does not match")
	}
	if _, ok := p.Lookup("F", sum(3), 1); ok {
		t.Fatal("stale F@1 matched F@3")
	}
	s := p.Summarize()
	if s.Hot(10) {
		t.Error("Hot(10) = true: the summary left the stale record out")
	}
	if s.Cold(10) {
		t.Error("Cold(10) = true")
	}
	if !s.Hot(1e9) {
		t.Error("Hot(1e9) = false")
	}

	// The stated cost: a profile merged across two builds holds F at both
	// checksums, F counts twice, and G drops out of the hot set. Total 2.2e9,
	// 90% is 1.98e9, reached by the two F records alone. With F once, the
	// total would be 1.2e9 and G would be needed to reach 1.08e9.
	merged := &Profile{Records: []Record{
		{Key: "F", Checksum: sum(1), Counts: []uint64{1e9}},
		{Key: "F", Checksum: sum(2), Counts: []uint64{1e9}},
		{Key: "G", Checksum: sum(3), Counts: []uint64{2e8}},
	}}
	if err := merged.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if merged.Summarize().Hot(2e8) {
		t.Error("Hot(2e8) = true on the merged profile")
	}
	once := &Profile{Records: []Record{merged.Records[0], merged.Records[2]}}
	if !once.Summarize().Hot(2e8) {
		t.Error("Hot(2e8) = false with F counted once")
	}

	// The summary is a function of the profile alone: equal hashes, equal
	// summaries.
	q := &Profile{Records: []Record{
		{Key: "F", Checksum: sum(1), Counts: []uint64{1e9}},
		{Key: "G", Checksum: sum(2), Counts: []uint64{10}},
	}}
	if p.Hash() != q.Hash() || *p.Summarize() != *q.Summarize() {
		t.Error("equal profiles gave different hashes or summaries")
	}
}

// TestHotTies: every entry count that ties with the last member of the
// shortest 90% prefix is hot, so the hot set can exceed that prefix; counts
// below it are not hot.
func TestHotTies(t *testing.T) {
	t.Parallel()
	hotCount := func(p *Profile) int {
		s := p.Summarize()
		n := 0
		for _, r := range p.Records {
			if s.Hot(r.Counts[0]) {
				n++
			}
		}
		return n
	}
	// Ten records of 10: the shortest prefix is nine, all ten are hot.
	if n := hotCount(entries(10, 10, 10, 10, 10, 10, 10, 10, 10, 10)); n != 10 {
		t.Errorf("ten ties: %d hot, want 10", n)
	}
	// 100 and a thousand 1s: total 1100, 90% is 990, the shortest prefix is
	// 100 and 890 ones; every 1 ties, so all 1001 are hot.
	counts := []uint64{100}
	for range 1000 {
		counts = append(counts, 1)
	}
	if n := hotCount(entries(counts...)); n != 1001 {
		t.Errorf("100 and 1000 ones: %d hot, want 1001", n)
	}
	// Without a tie, the set is exactly the shortest prefix: 90 of 100
	// reaches 90% alone.
	s := entries(90, 10).Summarize()
	if !s.Hot(90) || s.Hot(10) {
		t.Errorf("90, 10: Hot(90) = %v, Hot(10) = %v, want true, false", s.Hot(90), s.Hot(10))
	}
}
