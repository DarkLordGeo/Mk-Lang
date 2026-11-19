// Why: a pack and the protocol replies around it are untrusted input.
// What: FuzzPktLine (with the discovery, ls-refs and fetch-reply parsers above
// it), FuzzPack, FuzzDelta, FuzzTree (with the walk) and FuzzPackRecords.
// How: the property is no panic, no hang, and every failure an *Error. Limits
// are small so mutation reaches them; the pack target rewrites the trailer so
// inputs get past the SHA-1 check. Seeds come from the synthetic helper.

package git

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustGitError(t *testing.T, err error) {
	t.Helper()
	var ge *Error
	if err != nil && !errors.As(err, &ge) {
		t.Fatalf("a failure that is not a *git.Error: %T %v", err, err)
	}
}

func FuzzPktLine(f *testing.F) {
	for _, fx := range syntheticFixtures() {
		for _, b := range [][]byte{fx.discovery, fx.lsRefs, fx.fetch} {
			if b != nil {
				f.Add(b)
			}
		}
	}
	f.Add([]byte("0000000100020003fff1"))
	f.Fuzz(func(t *testing.T, data []byte) {
		pr := NewPktReader(bytes.NewReader(data))
		for i := 0; i < 1<<16; i++ {
			_, _, err := pr.Next()
			if err != nil {
				if err.Error() != "EOF" {
					mustGitError(t, err)
				}
				break
			}
		}
		_, err := ParseDiscovery(data)
		mustGitError(t, err)
		_, err = ParseLsRefs(data)
		mustGitError(t, err)
		_, err = ReadFetchReply(bytes.NewReader(data), 1<<20)
		mustGitError(t, err)
	})
}

// fuzzLimits keep one input's work small.
var fuzzLimits = Limits{MaxObjects: 1000, MaxBytes: 1 << 20, MaxDeltaChain: 50, MaxDepth: 16, MaxFiles: 1000}

func FuzzPack(f *testing.F) {
	for _, fx := range syntheticFixtures() {
		if fx.fetch != nil {
			if pack, err := ReadFetchReply(bytes.NewReader(fx.fetch), DefaultMaxBytes); err == nil {
				f.Add(pack[:len(pack)-20])
			}
		}
	}
	f.Add(packBody([]pobj{{typ: ObjBlob, data: []byte("a")}, {typ: objOfsDelta, data: makeDelta([]byte("a"), []byte("ab")), ofs: 0}}))
	f.Fuzz(func(t *testing.T, body []byte) {
		s := sha1.Sum(body)
		pack := append(body[:len(body):len(body)], s[:]...)
		st, err := ParsePack(pack, fuzzLimits)
		mustGitError(t, err)
		if err != nil {
			return
		}
		for o, obj := range st.objs {
			if HashObject(obj.Type, obj.Data) != o {
				t.Fatalf("object %s stored under a wrong id", o)
			}
			if obj.Type == ObjCommit {
				_, err := st.Files(o, fuzzLimits)
				mustGitError(t, err)
			}
			if obj.Type == ObjTag {
				_, err := st.Peel(o)
				mustGitError(t, err)
			}
		}
	})
}

func FuzzDelta(f *testing.F) {
	base := []byte("the quick brown fox jumps over the lazy dog\n")
	f.Add(base, makeDelta(base, append(append([]byte{}, base...), "again\n"...)))
	f.Add(base, append([]byte{byte(len(base)), 5}, deltaCopy(4, 5)...))
	f.Add([]byte{}, []byte{0, 0})
	f.Fuzz(func(t *testing.T, base, delta []byte) {
		out, err := ApplyDelta(base, delta, 1<<20)
		mustGitError(t, err)
		if err == nil && len(out) > 1<<20 {
			t.Fatalf("a result of %d bytes over the limit", len(out))
		}
	})
}

func FuzzTree(f *testing.F) {
	b := HashObject(ObjBlob, []byte("x"))
	f.Add(treeBytes([]tent{{"100644", "a", b}, {"40000", "d", b}, {"120000", "l", b}}))
	f.Add([]byte("100644 \x00"))
	for _, name := range []string{"ok-annotated", "link", "submodule"} {
		if p, err := os.ReadFile(filepath.Join(syntheticDir, name, "fetch.pkt")); err == nil {
			if pack, err := ReadFetchReply(bytes.NewReader(p), DefaultMaxBytes); err == nil {
				if st, err := ParsePack(pack, Limits{}); err == nil {
					for _, obj := range st.objs {
						if obj.Type == ObjTree {
							f.Add(obj.Data)
						}
					}
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		ents, err := ParseTree(data)
		mustGitError(t, err)
		if err != nil {
			return
		}
		// Walk it as the root of a commit. Each entry's id names an object
		// in the store: a blob for a file mode, this very tree for a
		// directory, so the walk recurses into itself (impossible in git,
		// so only the limits stop it).
		blob := []byte("x")
		st := &Store{objs: map[OID]Object{}}
		root := HashObject(ObjTree, data)
		for _, e := range ents {
			if e.Mode == ModeDir {
				st.objs[e.OID] = Object{Type: ObjTree, Data: data}
			} else {
				st.objs[e.OID] = Object{Type: ObjBlob, Data: blob}
			}
		}
		st.objs[root] = Object{Type: ObjTree, Data: data}
		commit := commitBytes(root, "c")
		st.objs[HashObject(ObjCommit, commit)] = Object{Type: ObjCommit, Data: commit}
		_, err = st.Files(HashObject(ObjCommit, commit), fuzzLimits)
		mustGitError(t, err)
	})
}

// FuzzPackRecords builds a valid pack from records read off the input, three
// bytes and content each: a kind (commit, tree, blob, tag, offset delta,
// reference delta), a base index and a length. A delta's result is its base's
// content followed by its own, written with copy and insert instructions;
// reference deltas go first in the pack, ahead of their bases. Every record
// must then parse to its id, so mutation exercises inflation, delta resolution
// and the walk rather than stopping at zlib.
func FuzzPackRecords(f *testing.F) {
	f.Add([]byte("\x02\x00\x05hello\x04\x00\x03 you\x05\x01\x01!\x00\x00\x00"))
	f.Add([]byte("\x02\x00\x01a\x05\x00\x01b\x05\x01\x01c\x04\x02\x01d"))
	f.Fuzz(func(t *testing.T, data []byte) {
		type rec struct {
			typ     ObjectType // the resolved type
			kind    ObjectType // as written
			base    int
			content []byte
		}
		var recs []rec
		for pos := 0; pos+3 <= len(data) && len(recs) < 64; {
			kind, base, n := data[pos]%6, int(data[pos+1]), int(data[pos+2])
			pos += 3
			n = min(n, len(data)-pos)
			own := data[pos : pos+n]
			pos += n
			r := rec{kind: ObjectType(kind + 1), content: own}
			if kind >= 4 {
				if len(recs) == 0 {
					r.kind = ObjBlob
				} else {
					r.kind = map[byte]ObjectType{4: objOfsDelta, 5: objRefDelta}[kind]
					r.base = base % len(recs)
					b := recs[r.base]
					r.typ = b.typ
					r.content = append(append([]byte{}, b.content...), own...)
				}
			}
			if r.kind <= ObjTag {
				r.typ = r.kind
			}
			recs = append(recs, r)
		}
		if len(recs) == 0 {
			return
		}
		// Emission order: reference deltas first, then the rest.
		var order []int
		for i, r := range recs {
			if r.kind == objRefDelta {
				order = append(order, i)
			}
		}
		for i, r := range recs {
			if r.kind != objRefDelta {
				order = append(order, i)
			}
		}
		at := make([]int, len(recs))
		for p, i := range order {
			at[i] = p
		}
		objs := make([]pobj, len(order))
		for p, i := range order {
			r := recs[i]
			switch r.kind {
			case objOfsDelta:
				objs[p] = pobj{typ: objOfsDelta, data: makeDelta(recs[r.base].content, r.content), ofs: at[r.base]}
			case objRefDelta:
				b := recs[r.base]
				objs[p] = pobj{typ: objRefDelta, data: makeDelta(b.content, r.content), refBase: HashObject(b.typ, b.content)}
			default:
				objs[p] = pobj{typ: r.kind, data: r.content}
			}
		}
		lim := Limits{MaxObjects: 100, MaxBytes: 16 << 20, MaxDeltaChain: 100, MaxDepth: 16, MaxFiles: 1000}
		st, err := ParsePack(writePack(objs), lim)
		if err != nil {
			t.Fatalf("a valid pack of %d records: %v", len(recs), err)
		}
		for i, r := range recs {
			o := HashObject(r.typ, r.content)
			if obj, ok := st.Get(o); !ok || obj.Type != r.typ || !bytes.Equal(obj.Data, r.content) {
				t.Fatalf("record %d (%s as %s) is not in the store under its id", i, r.typ, r.kind)
			}
			if r.typ == ObjCommit {
				_, err := st.Files(o, lim)
				mustGitError(t, err)
			}
		}
	})
}
