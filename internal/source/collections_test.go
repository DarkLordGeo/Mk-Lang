// Why: the compiler's output must not depend on map order.
// What: TestIndexMapOrder, TestIndexMapReplaceInPlace and TestIndexSet.
// How: checks insertion order across inserts, replacements and lookups.

package source

import (
	"slices"
	"testing"
)

func TestIndexMapOrder(t *testing.T) {
	t.Parallel()
	var m IndexMap[string, int] // zero value is usable
	for i, k := range []string{"c", "a", "b"} {
		if _, had := m.Insert(k, i); had {
			t.Fatalf("%q reported present", k)
		}
	}
	if got := slices.Collect(m.Keys()); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("Keys = %v", got)
	}
	if got := slices.Collect(m.Values()); !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("Values = %v", got)
	}
	i := 0
	for k, v := range m.All() {
		if k2, v2 := m.At(i); k2 != k || v2 != v || m.KeyAt(i) != k || m.ValueAt(i) != v {
			t.Fatalf("All/At disagree at %d", i)
		}
		i++
	}
	if idx, ok := m.IndexOf("a"); !ok || idx != 1 {
		t.Fatalf("IndexOf = %d %v", idx, ok)
	}
	if _, ok := m.IndexOf("zz"); ok || m.Has("zz") || m.Ptr("zz") != nil {
		t.Fatal("absent key")
	}
}

func TestIndexMapReplaceInPlace(t *testing.T) {
	t.Parallel()
	var m IndexMap[string, int]
	m.Insert("x", 1)
	m.Insert("y", 2)
	if old, had := m.Insert("x", 10); !had || old != 1 {
		t.Fatalf("replace returned %d %v", old, had)
	}
	if k, v := m.At(0); k != "x" || v != 10 || m.Len() != 2 {
		t.Fatalf("At(0) = %q %d, Len %d", k, v, m.Len())
	}
	*m.Ptr("y") = 20
	if v, _ := m.Get("y"); v != 20 {
		t.Fatalf("Ptr write lost: %d", v)
	}
	p, ins := m.Entry("z")
	if !ins || *p != 0 {
		t.Fatal("Entry insert")
	}
	*p = 30
	if p2, ins2 := m.Entry("z"); ins2 || *p2 != 30 {
		t.Fatal("Entry existing")
	}
	c := m.Clone()
	m.Insert("x", 99)
	if v, _ := c.Get("x"); v != 10 {
		t.Fatal("Clone shares storage")
	}
	m.Clear()
	if m.Len() != 0 || m.Has("x") {
		t.Fatal("Clear")
	}
	m.Insert("q", 1)
	if k, _ := m.At(0); k != "q" {
		t.Fatal("insert after Clear")
	}
}

func TestIndexSet(t *testing.T) {
	t.Parallel()
	var s IndexSet[int]
	for _, v := range []int{5, 3, 5, 9, 3} {
		s.Insert(v)
	}
	if !slices.Equal(s.Slice(), []int{5, 3, 9}) || s.Len() != 3 {
		t.Fatalf("Slice = %v", s.Slice())
	}
	if s.Insert(9) || !s.Insert(1) || !s.Has(1) || s.At(3) != 1 {
		t.Fatal("Insert/Has/At")
	}
	if i, ok := s.IndexOf(9); !ok || i != 2 {
		t.Fatal("IndexOf")
	}
	if got := slices.Collect(s.All()); !slices.Equal(got, []int{5, 3, 9, 1}) {
		t.Fatalf("All = %v", got)
	}
	c := s.Clone()
	s.Clear()
	if s.Len() != 0 || c.Len() != 4 {
		t.Fatal("Clear/Clone")
	}
}
