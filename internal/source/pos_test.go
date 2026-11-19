// Why: spans are the positions every diagnostic reports.
// What: TestSpanBasics, TestSpanToAcrossFilesIsICE, TestNewSpanReversedIsICE
// and TestPositionString.
// How: builds spans and checks their misuse is an ICE.

package source

import "testing"

func TestSpanBasics(t *testing.T) {
	t.Parallel()
	s := NewSpan(1, 3, 7)
	if s.Len() != 4 || s.IsEmpty() || !s.IsValid() {
		t.Fatalf("span %v: Len %d IsEmpty %v IsValid %v", s, s.Len(), s.IsEmpty(), s.IsValid())
	}
	if (Span{}).IsValid() || !(Span{}).IsEmpty() {
		t.Fatal("zero span must be invalid and empty")
	}
	if got := s.To(NewSpan(1, 10, 12)); got != (Span{File: 1, Start: 3, End: 12}) {
		t.Fatalf("To = %v", got)
	}
	if got := s.To(Span{}); got != s {
		t.Fatalf("To(zero) = %v", got)
	}
	if got := (Span{}).To(s); got != s {
		t.Fatalf("zero.To = %v", got)
	}
	if !s.Contains(Pos{File: 1, Off: 7}) || s.Contains(Pos{File: 1, Off: 8}) || s.Contains(Pos{File: 2, Off: 4}) {
		t.Fatal("Contains")
	}
	if s.StartPos() != (Pos{1, 3}) || s.EndPos() != (Pos{1, 7}) {
		t.Fatal("StartPos/EndPos")
	}
	if p := (Pos{File: 1, Off: 5}); p.Span() != (Span{1, 5, 5}) || !p.IsValid() {
		t.Fatal("Pos.Span")
	}
}

func TestSpanToAcrossFilesIsICE(t *testing.T) {
	t.Parallel()
	ie := Guard("test", Span{}, func() { NewSpan(1, 0, 1).To(NewSpan(2, 0, 1)) })
	if ie == nil || ie.Runtime {
		t.Fatalf("want ICE, got %+v", ie)
	}
}

func TestNewSpanReversedIsICE(t *testing.T) {
	t.Parallel()
	if ie := Guard("test", Span{}, func() { NewSpan(1, 5, 4) }); ie == nil || ie.Runtime {
		t.Fatalf("want ICE, got %+v", ie)
	}
}

func TestPositionString(t *testing.T) {
	t.Parallel()
	if got := (Position{Path: "a/b.mk", LineCol: LineCol{3, 14}}).String(); got != "a/b.mk:3:14" {
		t.Fatalf("got %q", got)
	}
	if got := (Position{}).String(); got != "-" {
		t.Fatalf("zero Position = %q", got)
	}
	if got := (LineCol{1, 2}).String(); got != "1:2" {
		t.Fatalf("LineCol = %q", got)
	}
}
