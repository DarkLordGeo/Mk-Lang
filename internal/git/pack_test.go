// Why: the pack reader decodes untrusted bytes into objects the build trusts.
// What: tests of pkt-lines, ls-refs, packs, deltas, tree entries, commit and
// tag parsing, bad object ids and the walk limits (TestGit*,
// TestGitWalkLimits).
// How: synthetic packs from the helper in synth_test.go cover offset and
// reference deltas, chains, every refused entry type and a bad trailer; each
// limit is a reason value, never a panic.

package git

import (
	"bytes"
	"strings"
	"testing"
)

func TestGitPktLines(t *testing.T) {
	read := func(s string) ([]PktKind, []string, error) {
		pr := NewPktReader(strings.NewReader(s))
		var ks []PktKind
		var ds []string
		for {
			k, b, err := pr.Next()
			if err != nil {
				if err.Error() == "EOF" {
					return ks, ds, nil
				}
				return ks, ds, err
			}
			ks = append(ks, k)
			ds = append(ds, string(b))
		}
	}
	ks, ds, err := read("0009done\n000000010002")
	if err != nil || len(ks) != 4 || ks[0] != PktData || ds[0] != "done\n" || ks[1] != PktFlush || ks[2] != PktDelim || ks[3] != PktResponseEnd {
		t.Errorf("kinds %v data %q err %v", ks, ds, err)
	}
	for _, bad := range []string{"0003", "fff1" + strings.Repeat("x", 0xfff1-4), "00zz", "000ashort", "00"} {
		if _, _, err := read(bad); !isReason(err, ReasonCorrupt) {
			t.Errorf("%.12q: %v; want corrupt", bad, err)
		}
	}
	max := "fff0" + strings.Repeat("y", MaxPktLen-4)
	if _, ds, err := read(max); err != nil || len(ds[0]) != MaxPktLen-4 {
		t.Errorf("a line of 65,520: %v", err)
	}
}

// TestGitPackDeltas: whole objects, a reference delta before its base,
// and an offset-delta chain, all resolved with their ids recomputed.
func TestGitPackDeltas(t *testing.T) {
	base := []byte(strings.Repeat("base line\n", 100))
	var objs []pobj
	prev := base
	objs = append(objs, pobj{typ: objRefDelta, data: makeDelta(base, append(append([]byte{}, base...), "ref\n"...)), refBase: HashObject(ObjBlob, base)})
	objs = append(objs, pobj{typ: ObjBlob, data: base})
	want := []OID{HashObject(ObjBlob, append(append([]byte{}, base...), "ref\n"...)), HashObject(ObjBlob, base)}
	for i := 0; i < 20; i++ {
		next := append(append([]byte{}, prev...), byte('a'+i), '\n')
		objs = append(objs, pobj{typ: objOfsDelta, data: makeDelta(prev, next), ofs: len(objs) - 1})
		if i == 0 {
			objs[len(objs)-1].ofs = 1
		}
		want = append(want, HashObject(ObjBlob, next))
		prev = next
	}
	st, err := ParsePack(writePack(objs), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range want {
		if obj, ok := st.Get(o); !ok || obj.Type != ObjBlob {
			t.Errorf("object %s missing", o)
		}
	}
	if _, err := ParsePack(writePack(objs), Limits{MaxDeltaChain: 19}); !isReason(err, ReasonLimit) {
		t.Errorf("a chain of 20 under a limit of 19: %v", err)
	}
	if _, err := ParsePack(writePack(objs), Limits{MaxDeltaChain: 20}); err != nil {
		t.Errorf("a chain of 20 under a limit of 20: %v", err)
	}
}

func TestGitPackRefusals(t *testing.T) {
	blob := []byte("hello\n")
	good := writePack([]pobj{{typ: ObjBlob, data: blob}})
	badTrailer := append([]byte{}, good...)
	badTrailer[len(badTrailer)-1] ^= 1
	trailing := withTrailer(append(packBody([]pobj{{typ: ObjBlob, data: blob}}), 0))
	missingBase := writePack([]pobj{{typ: objRefDelta, data: makeDelta(blob, []byte("x")), refBase: HashObject(ObjBlob, []byte("other"))}})
	// A declared size that differs from the inflated one.
	body := packBody([]pobj{{typ: ObjBlob, data: blob}})
	body[12] = byte(ObjBlob)<<4 | 5 // size 5 instead of 6
	wrongSize := withTrailer(body)
	// An offset delta that points before the pack's first object.
	ofsBody := packBody([]pobj{{typ: ObjBlob, data: blob}, {typ: objOfsDelta, data: makeDelta(blob, []byte("y")), ofs: 0}})
	badOfs := append([]byte{}, ofsBody...)
	hdr := 12 + len(typeSize(ObjBlob, len(blob))) + len(zlibBytes(blob))
	badOfs[hdr+len(typeSize(objOfsDelta, len(makeDelta(blob, []byte("y")))))] = 0x7f // 127 bytes back
	type5 := packBody([]pobj{{typ: ObjBlob, data: blob}})
	type5[12] = 5<<4 | 6
	for name, tc := range map[string]struct {
		pack   []byte
		lim    Limits
		reason string
	}{
		"bad trailer":       {badTrailer, Limits{}, ReasonCorrupt},
		"bytes after":       {trailing, Limits{}, ReasonCorrupt},
		"missing base":      {missingBase, Limits{}, ReasonCorrupt},
		"declared size":     {wrongSize, Limits{}, ReasonCorrupt},
		"offset before":     {withTrailer(badOfs), Limits{}, ReasonCorrupt},
		"type 5":            {withTrailer(type5), Limits{}, ReasonCorrupt},
		"not a pack":        {withTrailer([]byte("JUNK\x00\x00\x00\x02\x00\x00\x00\x00")), Limits{}, ReasonCorrupt},
		"version 4":         {withTrailer([]byte("PACK\x00\x00\x00\x04\x00\x00\x00\x00")), Limits{}, ReasonCorrupt},
		"too many objects":  {withTrailer([]byte("PACK\x00\x00\x00\x02\x00\x0f\x42\x41")), Limits{}, ReasonLimit},
		"count over a body": {withTrailer([]byte("PACK\x00\x00\x00\x02\x00\x00\x00\x05")), Limits{}, ReasonCorrupt},
		"over the bytes":    {good, Limits{MaxBytes: 5}, ReasonLimit},
		"objects limit":     {writePack([]pobj{{typ: ObjBlob, data: blob}, {typ: ObjBlob, data: blob}}), Limits{MaxObjects: 1}, ReasonLimit},
	} {
		if _, err := ParsePack(tc.pack, tc.lim); !isReason(err, tc.reason) {
			t.Errorf("%s: %v; want %s", name, err, tc.reason)
		}
	}
	if _, err := ParsePack(withTrailer([]byte("PACK\x00\x00\x00\x02\x00\x0f\x42\x40")), Limits{}); isReason(err, ReasonLimit) {
		t.Errorf("1,000,000 objects is at the limit, not over it: %v", err)
	}
}

func TestGitDelta(t *testing.T) {
	base := []byte("0123456789")
	for name, tc := range map[string]struct {
		delta []byte
		want  string
		ok    bool
	}{
		"copy and insert": {append(append([]byte{10, 7}, deltaCopy(2, 4)...), deltaInsert([]byte("abc"))...), "2345abc", true},
		"wrong base size": {append([]byte{9, 1}, deltaInsert([]byte("a"))...), "", false},
		"reserved 0":      {[]byte{10, 1, 0}, "", false},
		"copy past base":  {append([]byte{10, 4}, deltaCopy(8, 4)...), "", false},
		"short result":    {append([]byte{10, 5}, deltaInsert([]byte("ab"))...), "", false},
		"long result":     {append([]byte{10, 1}, deltaInsert([]byte("ab"))...), "", false},
		"cut insert":      {[]byte{10, 3, 3, 'a'}, "", false},
		"cut copy":        {[]byte{10, 3, 0x91}, "", false},
		"cut header":      {[]byte{0x80}, "", false},
	} {
		got, err := ApplyDelta(base, tc.delta, 1<<20)
		if tc.ok && (err != nil || string(got) != tc.want) || !tc.ok && !isReason(err, ReasonCorrupt) {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	// A declared result over the budget is a limit, not an allocation.
	huge := append(append([]byte{10}, deltaVarintBytes(1<<40)...), deltaCopy(0, 10)...)
	if _, err := ApplyDelta(base, huge, 1<<20); !isReason(err, ReasonLimit) {
		t.Errorf("a 1 TiB result: %v", err)
	}
	// A copy of size 0 means 0x10000.
	big := bytes.Repeat([]byte{'z'}, 0x10000)
	got, err := ApplyDelta(big, append(append(deltaVarintBytes(0x10000), deltaVarintBytes(0x10000)...), 0x80), 1<<20)
	if err != nil || !bytes.Equal(got, big) {
		t.Errorf("size 0 copy: %d bytes, %v", len(got), err)
	}
}

// treePack builds a pack holding root and the commit naming it, plus objs.
func treePack(root []byte, objs ...pobj) (*Store, OID) {
	rootOID := HashObject(ObjTree, root)
	commit := commitBytes(rootOID, "c")
	all := append(objs, pobj{typ: ObjTree, data: root}, pobj{typ: ObjCommit, data: commit})
	st, err := ParsePack(writePack(all), Limits{})
	if err != nil {
		panic(err)
	}
	return st, HashObject(ObjCommit, commit)
}

func TestGitTreeEntries(t *testing.T) {
	blob := []byte("x\n")
	b := HashObject(ObjBlob, blob)
	wb := pobj{typ: ObjBlob, data: blob}
	for name, tc := range map[string]struct {
		ents   []tent
		reason string
	}{
		"file":           {[]tent{{"100644", "a", b}}, ""},
		"executable":     {[]tent{{"100755", "a", b}}, ""},
		"old 100664":     {[]tent{{"100664", "a", b}}, ""},
		"link":           {[]tent{{"120000", "a", b}}, ReasonRefused},
		"submodule":      {[]tent{{"160000", "a", b}}, ReasonRefused},
		"mode 100600":    {[]tent{{"100600", "a", b}}, ReasonRefused},
		"dot git":        {[]tent{{"100644", ".GIT", b}}, ReasonRefused},
		"missing blob":   {[]tent{{"100644", "a", HashObject(ObjBlob, []byte("absent"))}}, ReasonCorrupt},
		"duplicate name": {[]tent{{"100644", "a", b}, {"100755", "a", b}}, ReasonCorrupt},
		"dot dot":        {[]tent{{"100644", "..", b}}, ReasonCorrupt},
	} {
		st, c := treePack(treeBytes(tc.ents), wb)
		_, err := st.Files(c, Limits{})
		if tc.reason == "" && err != nil || tc.reason != "" && !isReason(err, tc.reason) {
			t.Errorf("%s: %v; want %q", name, err, tc.reason)
		}
	}
}

// TestGitBadObjectID: a commit names a tree id whose object in the pack
// has other content; the recomputed id does not match, so the tree is not
// found.
func TestGitBadObjectID(t *testing.T) {
	blob := []byte("x\n")
	real := treeBytes([]tent{{"100644", "a", HashObject(ObjBlob, blob)}})
	claimed := HashObject(ObjTree, []byte("something else"))
	commit := commitBytes(claimed, "c")
	st, err := ParsePack(writePack([]pobj{{typ: ObjBlob, data: blob}, {typ: ObjTree, data: real}, {typ: ObjCommit, data: commit}}), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Files(HashObject(ObjCommit, commit), Limits{}); !isReason(err, ReasonCorrupt) {
		t.Errorf("tree id mismatch: %v", err)
	}
}

// TestGitWalkLimits: depth, files and bytes are counted during the
// walk; a tree naming one subtree twice per level doubles per level.
func TestGitWalkLimits(t *testing.T) {
	blob := []byte("0123456789")
	b := HashObject(ObjBlob, blob)
	objs := []pobj{{typ: ObjBlob, data: blob}}
	level := treeBytes([]tent{{"100644", "f", b}})
	for i := 0; i < 20; i++ {
		objs = append(objs, pobj{typ: ObjTree, data: level})
		sub := HashObject(ObjTree, level)
		level = treeBytes([]tent{{"40000", "l", sub}, {"40000", "r", sub}})
	}
	st, c := treePack(level, objs...)
	if _, err := st.Files(c, Limits{}); !isReason(err, ReasonLimit) {
		t.Errorf("2^20 files: %v; want the files limit", err)
	}
	if _, err := st.Files(c, Limits{MaxFiles: 1 << 21, MaxBytes: 1 << 20}); !isReason(err, ReasonLimit) {
		t.Errorf("10 MiB of files under 1 MiB: %v", err)
	}
	if _, err := st.Files(c, Limits{MaxDepth: 19}); !isReason(err, ReasonLimit) {
		t.Errorf("depth 20 under 19: %v", err)
	}
	// Empty leaves: no file is ever reached, so only the entry count stops
	// 2^40 visits.
	empty := treeBytes(nil)
	objs = []pobj{{typ: ObjTree, data: empty}}
	level = empty
	for i := 0; i < 40; i++ {
		sub := HashObject(ObjTree, level)
		level = treeBytes([]tent{{"40000", "a", sub}, {"40000", "b", sub}})
		objs = append(objs, pobj{typ: ObjTree, data: level})
	}
	st, c = treePack(level, objs[:len(objs)-1]...)
	if _, err := st.Files(c, Limits{MaxFiles: 1000}); !isReason(err, ReasonLimit) {
		t.Errorf("2^40 empty directories: %v", err)
	}
	// Long names: path bytes count against the byte limit.
	long := strings.Repeat("n", 4000)
	objs = []pobj{{typ: ObjBlob, data: blob}}
	level = treeBytes([]tent{{"100644", long, b}})
	for i := 0; i < 12; i++ {
		objs = append(objs, pobj{typ: ObjTree, data: level})
		sub := HashObject(ObjTree, level)
		level = treeBytes([]tent{{"40000", long + "a", sub}, {"40000", long + "b", sub}})
	}
	st, c = treePack(level, objs...)
	if _, err := st.Files(c, Limits{MaxBytes: 64 << 20}); !isReason(err, ReasonLimit) {
		t.Errorf("long paths: %v", err)
	}
	// 256 nested directories pass; 257 do not.
	nest := func(n int) (*Store, OID) {
		objs := []pobj{{typ: ObjBlob, data: blob}}
		cur := treeBytes([]tent{{"100644", "f", b}})
		for i := 0; i < n; i++ {
			objs = append(objs, pobj{typ: ObjTree, data: cur})
			cur = treeBytes([]tent{{"40000", "d", HashObject(ObjTree, cur)}})
		}
		return treePack(cur, objs...)
	}
	if st, c := nest(256); true {
		if _, err := st.Files(c, Limits{}); err != nil {
			t.Errorf("256 nested directories: %v", err)
		}
	}
	if st, c := nest(257); true {
		if _, err := st.Files(c, Limits{}); !isReason(err, ReasonLimit) {
			t.Errorf("257 nested directories: %v", err)
		}
	}
}

func TestGitParseCommitTag(t *testing.T) {
	o := HashObject(ObjBlob, []byte("x"))
	if got, err := ParseCommit(commitBytes(o, "m")); err != nil || got != o {
		t.Errorf("commit: %v %v", got, err)
	}
	for _, bad := range []string{"", "parent " + o.String(), "tree zz"} {
		if _, err := ParseCommit([]byte(bad)); !isReason(err, ReasonCorrupt) {
			t.Errorf("commit %q: %v", bad, err)
		}
	}
	if got, typ, err := ParseTag(tagBytes(o, "v1")); err != nil || got != o || typ != "commit" {
		t.Errorf("tag: %v %v %v", got, typ, err)
	}
	if _, _, err := ParseTag([]byte("type commit\n")); !isReason(err, ReasonCorrupt) {
		t.Errorf("tag without object: %v", err)
	}
}

func TestGitLsRefs(t *testing.T) {
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 40)
	refs, err := ParseLsRefs(lsRefsReply(a+" refs/tags/v1.0.0 peeled:"+b, a+" refs/tags/v2.0.0 symref-target:x"))
	if err != nil || len(refs) != 2 || refs[0].Peeled.String() != b || refs[0].Commit().String() != b || refs[1].Commit().String() != a {
		t.Errorf("%v %v", refs, err)
	}
	if _, err := ParseLsRefs(lsRefsReply(strings.Repeat("c", 64) + " refs/tags/v1")); !isReason(err, ReasonProtocol) {
		t.Errorf("a SHA-256 id: %v", err)
	}
	if _, err := ParseLsRefs(lsRefsReply("ERR denied")); !isReason(err, ReasonNetwork) {
		t.Errorf("ERR: %v", err)
	}
	if _, err := ParseLsRefs([]byte("0009done\n")); !isReason(err, ReasonCorrupt) {
		t.Errorf("no flush: %v", err)
	}
}
