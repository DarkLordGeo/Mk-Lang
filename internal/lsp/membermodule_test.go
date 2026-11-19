// Why: two std modules may declare a type of one name (net.Conn and
// tls.Conn), and member completion tops up a capped query answer from the
// declarations; that top-up must stay inside the type's own module.
//
// What: TestLSPCompletionMembersStayInModule.
//
// How: completion after a net.Conn, a tls.Conn, an inferred array local
// and a std module name, each checked for members that belong elsewhere.

package lsp

import (
	"path/filepath"
	"testing"
)

const memberModuleFixture = `import std.fs
import std.net
import std.tls

Peer(c: &net.Conn): bool {
    return c.PeerAddr().IsSome()
}

Proto(t: &tls.Conn): bool {
    return t.Alpn().IsSome()
}

Sum(): int {
    xs := [1, 2, 3]
    return xs.Len()
}

export Main(sys: &System) {
    _ = fs.ReadText(sys.arena, sys.io, "x").Must()
    _ = Sum()
    buf := sys.arena.Slice[u8](4096).Must()
    _ = buf.Len()
}
`

// TestLSPCompletionMembersStayInModule: net.Conn offers PeerAddr and not
// tls.Conn's Alpn, tls.Conn offers Alpn and not net.Conn's PeerAddr, an
// inferred `[3]int` or `[]u8` local offers Slice's Len, and `fs.` offers the
// module's functions and types but no struct field.
func TestLSPCompletionMembersStayInModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.mk")
	must(t, writeFile(path, memberModuleFixture))
	uri := pathURI(path)
	c := start(t)
	c.init(true)
	c.open(uri, memberModuleFixture)
	if p := c.diagnostics(uri); len(p.Diagnostics) != 0 {
		t.Fatalf("fixture has diagnostics: %+v", p.Diagnostics)
	}
	for _, tc := range []struct {
		sub        string
		off        int
		want, deny []string
	}{
		{"c.PeerAddr", 2, []string{"PeerAddr", "Read", "WriteAll"}, []string{"Alpn"}},
		{"t.Alpn", 2, []string{"Alpn"}, []string{"PeerAddr"}},
		{"xs.Len", 3, []string{"Len"}, nil},
		{"buf.Len", 4, []string{"Len"}, nil},
		{"fs.ReadText", 3, []string{"ReadText", "WriteAll"}, []string{"is_dir", "size", "modified"}},
	} {
		got := completeAt(t, c, uri, memberModuleFixture, tc.sub, tc.off)
		for _, w := range tc.want {
			if _, ok := got[w]; !ok {
				t.Errorf("completion at %q lacks %s (%d items)", tc.sub, w, len(got))
			}
		}
		for _, d := range tc.deny {
			if _, ok := got[d]; ok {
				t.Errorf("completion at %q offers %s, which is not its member", tc.sub, d)
			}
		}
	}
	c.shutdown()
}
