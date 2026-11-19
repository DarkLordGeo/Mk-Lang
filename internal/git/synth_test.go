// Why: the client's tests need small, exact git wire data without a server.
// What: the synthetic pack and reply writer, syntheticFixtures and
// TestSyntheticFixturesCurrent.
// How: the fixtures live under testdata/synthetic, served by httptest here and
// in internal/fetch; the test holds them equal to the writer (-update
// rewrites).

package git

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/synthetic from the helper")

// pobj is one object for writePack: a whole object (typ, data), an offset
// delta on the object at index base (ofs), or a reference delta on refBase.
type pobj struct {
	typ     ObjectType
	data    []byte // whole content, or delta instructions
	ofs     int    // for objOfsDelta: index of the base in the pack
	refBase OID    // for objRefDelta
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// typeSize is the pack's type and size varint.
func typeSize(t ObjectType, size int) []byte {
	b := []byte{byte(t)<<4 | byte(size&15)}
	size >>= 4
	for size != 0 {
		b[len(b)-1] |= 0x80
		b = append(b, byte(size&0x7f))
		size >>= 7
	}
	return b
}

// ofsBytes is git's offset-delta encoding of back (inverse of the reader).
func ofsBytes(back int) []byte {
	buf := []byte{byte(back & 127)}
	for back >>= 7; back != 0; back >>= 7 {
		back--
		buf = append([]byte{byte(128 | back&127)}, buf...)
	}
	return buf
}

// packBody writes the header and objects, without the trailer.
func packBody(objs []pobj) []byte {
	var out bytes.Buffer
	out.WriteString("PACK")
	binary.Write(&out, binary.BigEndian, uint32(2))
	binary.Write(&out, binary.BigEndian, uint32(len(objs)))
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = out.Len()
		out.Write(typeSize(o.typ, len(o.data)))
		switch o.typ {
		case objOfsDelta:
			out.Write(ofsBytes(offs[i] - offs[o.ofs]))
		case objRefDelta:
			out.Write(o.refBase[:])
		}
		out.Write(zlibBytes(o.data))
	}
	return out.Bytes()
}

// writePack is a whole pack with its SHA-1 trailer.
func writePack(objs []pobj) []byte { return withTrailer(packBody(objs)) }

func withTrailer(body []byte) []byte {
	s := sha1.Sum(body)
	return append(append([]byte{}, body...), s[:]...)
}

func deltaVarintBytes(v int) []byte {
	var b []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b = append(b, c|0x80)
			continue
		}
		return append(b, c)
	}
}

// deltaCopy is one copy instruction (size 0x10000 written as no size bytes).
func deltaCopy(off, size int) []byte {
	op := byte(0x80)
	var args []byte
	for i := 0; i < 4; i++ {
		if b := byte(off >> (8 * i)); b != 0 {
			op |= 1 << i
			args = append(args, b)
		}
	}
	if size != 0x10000 {
		for i := 0; i < 3; i++ {
			if b := byte(size >> (8 * i)); b != 0 {
				op |= 0x10 << i
				args = append(args, b)
			}
		}
	}
	return append([]byte{op}, args...)
}

func deltaInsert(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		n := min(len(b), 127)
		out = append(out, byte(n))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out
}

// makeDelta turns base into target: a copy of the common prefix, then the
// rest inserted.
func makeDelta(base, target []byte) []byte {
	d := append(deltaVarintBytes(len(base)), deltaVarintBytes(len(target))...)
	p := 0
	for p < len(base) && p < len(target) && base[p] == target[p] {
		p++
	}
	for off := 0; off < p; {
		n := min(p-off, 0x10000)
		d = append(d, deltaCopy(off, n)...)
		off += n
	}
	return append(d, deltaInsert(target[p:])...)
}

type tent struct {
	mode string
	name string
	oid  OID
}

// treeBytes writes a tree object in git's order (a directory sorts as its
// name followed by '/').
func treeBytes(ents []tent) []byte {
	key := func(e tent) string {
		if e.mode == "40000" {
			return e.name + "/"
		}
		return e.name
	}
	sort.Slice(ents, func(i, j int) bool { return key(ents[i]) < key(ents[j]) })
	var b bytes.Buffer
	for _, e := range ents {
		fmt.Fprintf(&b, "%s %s\x00", e.mode, e.name)
		b.Write(e.oid[:])
	}
	return b.Bytes()
}

func commitBytes(tree OID, msg string) []byte {
	return []byte("tree " + tree.String() + "\nauthor A <a@example.com> 1700000000 +0000\ncommitter A <a@example.com> 1700000000 +0000\n\n" + msg + "\n")
}

func tagBytes(obj OID, name string) []byte {
	return []byte("object " + obj.String() + "\ntype commit\ntag " + name + "\ntagger A <a@example.com> 1700000000 +0000\n\n" + name + "\n")
}

// repoBuilder collects whole objects by id.
type repoBuilder struct {
	objs []pobj
}

func (r *repoBuilder) add(t ObjectType, data []byte) OID {
	r.objs = append(r.objs, pobj{typ: t, data: data})
	return HashObject(t, data)
}

// discoveryReply is a v2 advertisement; service adds git-http-backend's
// "# service=" line and flush.
func discoveryReply(service bool, caps ...string) []byte {
	w := &PktWriter{}
	if service {
		w.Line("# service=git-upload-pack")
		w.Flush()
	}
	w.Line("version 2")
	for _, c := range caps {
		w.Line(c)
	}
	w.Flush()
	return w.Bytes()
}

var stdCaps = []string{"agent=git/2.45.0", "ls-refs=unborn", "fetch=shallow wait-for-done filter", "server-option", "object-format=sha1"}

func lsRefsReply(lines ...string) []byte {
	w := &PktWriter{}
	for _, l := range lines {
		w.Line(l)
	}
	w.Flush()
	return w.Bytes()
}

// fetchReply wraps pack in a packfile section on band 1, after a
// shallow-info section (deepen 1's) and with band 2 progress interleaved.
func fetchReply(pack []byte, shallow OID, progress bool) []byte {
	w := &PktWriter{}
	if !shallow.IsZero() {
		w.Line("shallow-info")
		w.Line("shallow " + shallow.String())
		w.Delim()
	}
	w.Line("packfile")
	if progress {
		w.Data(append([]byte{2}, "Enumerating objects: done.\n"...))
	}
	const chunk = 1000 // small, so a pack spans many pkt-lines
	for len(pack) > 0 {
		n := min(len(pack), chunk)
		w.Data(append([]byte{1}, pack[:n]...))
		pack = pack[n:]
		if progress {
			w.Data(append([]byte{2}, "Receiving objects\r"...))
		}
	}
	w.Flush()
	return w.Bytes()
}

// fixture is one synthetic repository's replies and the expected result.
type fixture struct {
	discovery, lsRefs, fetch []byte
	expect                   string
}

// expectText is the replay tests' expected result: the tag, the commit,
// and one line per file, "file <mode> <sha256 hex> <path>".
func expectText(tag string, commit OID, files []File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tag %s\ncommit %s\n", tag, commit)
	for _, f := range files {
		s := sha256.Sum256(f.Data)
		fmt.Fprintf(&b, "file %o %s %s\n", f.Mode, hex.EncodeToString(s[:]), f.Path)
	}
	return b.String()
}

// okRepo builds a repository whose tag v1.0.0 holds files reached through
// whole objects, offset deltas, a reference delta whose base comes later,
// and a three-link chain; annotated says whether the tag is an object.
func okRepo(annotated bool) (fx fixture) {
	readme := []byte("# demo\n\nA synthetic repository for mk's git tests.\n")
	a := []byte(strings.Repeat("fn a() {}\n", 40))
	b := append(append([]byte{}, a...), "fn b() {}\n"...)
	c := append(append([]byte{}, b...), "fn c() {}\n"...)
	d := append(append([]byte{}, c...), "fn d() {}\n"...)
	run := []byte("#!/bin/sh\necho run\n")
	manifest := []byte("const pkg = Package(name: \"demo\")\n")
	var objs []pobj
	add := func(o pobj) int { objs = append(objs, o); return len(objs) - 1 }
	aOID := HashObject(ObjBlob, a)
	bOID := HashObject(ObjBlob, b)
	cOID := HashObject(ObjBlob, c)
	dOID := HashObject(ObjBlob, d)
	// Order: b's reference delta comes before its base a; c is an offset
	// delta on b; d an offset delta on c (a chain a <- b <- c <- d).
	iB := add(pobj{typ: objRefDelta, data: makeDelta(a, b), refBase: aOID})
	add(pobj{typ: ObjBlob, data: a})
	iC := add(pobj{typ: objOfsDelta, data: makeDelta(b, c), ofs: iB})
	add(pobj{typ: objOfsDelta, data: makeDelta(c, d), ofs: iC})
	readmeOID := HashObject(ObjBlob, readme)
	add(pobj{typ: ObjBlob, data: readme})
	runOID := HashObject(ObjBlob, run)
	add(pobj{typ: ObjBlob, data: run})
	manOID := HashObject(ObjBlob, manifest)
	add(pobj{typ: ObjBlob, data: manifest})
	srcDeep := treeBytes([]tent{{"100644", "d.mk", dOID}})
	srcDeepOID := HashObject(ObjTree, srcDeep)
	add(pobj{typ: ObjTree, data: srcDeep})
	src := treeBytes([]tent{{"100644", "a.mk", aOID}, {"100644", "b.mk", bOID}, {"100644", "c.mk", cOID}, {"40000", "deep", srcDeepOID}})
	srcOID := HashObject(ObjTree, src)
	add(pobj{typ: ObjTree, data: src})
	bin := treeBytes([]tent{{"100755", "run.sh", runOID}})
	binOID := HashObject(ObjTree, bin)
	add(pobj{typ: ObjTree, data: bin})
	root := treeBytes([]tent{{"100644", "README.md", readmeOID}, {"100644", "pkg.mk", manOID}, {"40000", "src", srcOID}, {"40000", "bin", binOID}})
	rootOID := HashObject(ObjTree, root)
	add(pobj{typ: ObjTree, data: root})
	commit := commitBytes(rootOID, "v1.0.0")
	commitOID := HashObject(ObjCommit, commit)
	add(pobj{typ: ObjCommit, data: commit})
	refLine := commitOID.String() + " refs/tags/v1.0.0"
	if annotated {
		tag := tagBytes(commitOID, "v1.0.0")
		tagOID := HashObject(ObjTag, tag)
		add(pobj{typ: ObjTag, data: tag})
		refLine = tagOID.String() + " refs/tags/v1.0.0 peeled:" + commitOID.String()
	}
	pack := writePack(objs)
	fx.discovery = discoveryReply(annotated, stdCaps...)
	fx.lsRefs = lsRefsReply(commitOID.String()+" refs/tags/v0.9.0", refLine)
	fx.fetch = fetchReply(pack, commitOID, true)
	files := []File{
		{Path: "README.md", Mode: ModeFile, Data: readme},
		{Path: "bin/run.sh", Mode: ModeExecutable, Data: run},
		{Path: "pkg.mk", Mode: ModeFile, Data: manifest},
		{Path: "src/a.mk", Mode: ModeFile, Data: a},
		{Path: "src/b.mk", Mode: ModeFile, Data: b},
		{Path: "src/c.mk", Mode: ModeFile, Data: c},
		{Path: "src/deep/d.mk", Mode: ModeFile, Data: d},
	}
	fx.expect = expectText("v1.0.0", commitOID, files)
	return fx
}

// oneEntryRepo is a lightweight tag v1.0.0 on a commit whose root tree
// holds a pkg.mk and the given extra entries (their blobs added).
func oneEntryRepo(extra []tent, blobs map[string][]byte, expect string) fixture {
	r := &repoBuilder{}
	man := r.add(ObjBlob, []byte("const pkg = Package(name: \"x\")\n"))
	ents := []tent{{"100644", "pkg.mk", man}}
	for _, e := range extra {
		if data, ok := blobs[e.name]; ok {
			e.oid = r.add(ObjBlob, data)
		}
		ents = append(ents, e)
	}
	root := r.add(ObjTree, treeBytes(ents))
	commit := r.add(ObjCommit, commitBytes(root, "v1.0.0"))
	return fixture{
		discovery: discoveryReply(true, stdCaps...),
		lsRefs:    lsRefsReply(commit.String() + " refs/tags/v1.0.0"),
		fetch:     fetchReply(writePack(r.objs), commit, false),
		expect:    expect,
	}
}

// syntheticFixtures are the fixture sets, by directory name.
func syntheticFixtures() map[string]fixture {
	sub, _ := ParseOID(strings.Repeat("ab", 20))
	caseClash := oneEntryRepo([]tent{{"100644", "Read.txt", OID{}}, {"100644", "read.txt", OID{}}},
		map[string][]byte{"Read.txt": []byte("one\n"), "read.txt": []byte("two\n")}, "")
	// The git layer accepts two names that differ in case; fetch refuses
	// them. Its expect is the walk's.
	caseClash.expect = expectOf(caseClash)
	ok := okRepo(true)
	pack, _ := ReadFetchReply(bytes.NewReader(ok.fetch), DefaultMaxBytes)
	refs, _ := ParseLsRefs(ok.lsRefs)
	commit := refs[1].Peeled
	errLine := func(text string) []byte { w := &PktWriter{}; w.Line("ERR " + text); return w.Bytes() }
	// A reply ending in 0002 (response-end) instead of 0000.
	end0002 := ok
	end0002.fetch = append(bytes.TrimSuffix(append([]byte{}, ok.fetch...), []byte("0000")), "0002"...)
	// ERR and band 3 in each section of the fetch reply.
	shallowErr := &PktWriter{}
	shallowErr.Line("shallow-info")
	shallowErr.Line("shallow " + commit.String())
	shallowErr.Line("ERR shallow update refused")
	packErr := &PktWriter{}
	packErr.Line("shallow-info")
	packErr.Line("shallow " + commit.String())
	packErr.Delim()
	packErr.Line("packfile")
	packErr.Data(append([]byte{1}, pack[:100]...))
	packErr.Line("ERR upload-pack: aborting")
	band3 := &PktWriter{}
	band3.Line("shallow-info")
	band3.Line("shallow " + commit.String())
	band3.Delim()
	band3.Line("packfile")
	band3.Data(append([]byte{2}, "Counting objects: 7\n"...))
	band3.Data(append([]byte{3}, "fatal: git upload-pack: not our ref\n"...))
	with := func(f fixture, mod func(*fixture)) fixture { mod(&f); return f }
	return map[string]fixture{
		"ok-end-0002": end0002,
		"err-discovery": {
			discovery: errLine("Repository not found."),
			expect:    "error network\n",
		},
		"err-ls-refs":      with(ok, func(f *fixture) { f.lsRefs = errLine("ls-refs refused"); f.expect = "error network\n" }),
		"err-shallow-info": with(ok, func(f *fixture) { f.fetch = shallowErr.Bytes(); f.expect = "error network\n" }),
		"err-packfile":     with(ok, func(f *fixture) { f.fetch = packErr.Bytes(); f.expect = "error network\n" }),
		"band3-packfile":   with(ok, func(f *fixture) { f.fetch = band3.Bytes(); f.expect = "error network\n" }),
		"ok-annotated":     okRepo(true),
		"ok-lightweight":   okRepo(false),
		"link":             oneEntryRepo([]tent{{"120000", "evil", OID{}}}, map[string][]byte{"evil": []byte("../../etc/passwd")}, "error refused\n"),
		"submodule":        oneEntryRepo([]tent{{"160000", "vendored", sub}}, nil, "error refused\n"),
		"case-clash":       caseClash,
		"sha256": {
			discovery: discoveryReply(true, "agent=git/2.45.0", "ls-refs", "fetch=shallow", "object-format=sha256"),
			expect:    "error protocol\n",
		},
		"protocol-v0": {
			// A version 0 advertisement: the first ref and its capabilities.
			discovery: v0Advertisement(),
			expect:    "error protocol\n",
		},
	}
}

func v0Advertisement() []byte {
	w := &PktWriter{}
	w.Line("# service=git-upload-pack")
	w.Flush()
	w.Data([]byte(strings.Repeat("1", 40) + " HEAD\x00multi_ack side-band-64k ofs-delta\n"))
	w.Flush()
	return w.Bytes()
}

// expectOf runs the fixture through the parser directly (no HTTP) to give
// the expect text of a fixture whose files the helper does not list.
func expectOf(fx fixture) string {
	pack, err := ReadFetchReply(bytes.NewReader(fx.fetch), DefaultMaxBytes)
	if err != nil {
		return "error " + err.(*Error).Reason + "\n"
	}
	st, err := ParsePack(pack, Limits{})
	if err != nil {
		return "error " + err.(*Error).Reason + "\n"
	}
	refs, _ := ParseLsRefs(fx.lsRefs)
	c, _ := st.Peel(refs[0].OID)
	files, err := st.Files(c, Limits{})
	if err != nil {
		return "error " + err.(*Error).Reason + "\n"
	}
	return expectText("v1.0.0", c, files)
}

const syntheticDir = "testdata/synthetic"

// fixtureFiles maps a fixture's file names to their bytes.
func fixtureFiles(fx fixture) map[string][]byte {
	m := map[string][]byte{"expect.txt": []byte(fx.expect)}
	if fx.discovery != nil {
		m["discovery.pkt"] = fx.discovery
	}
	if fx.lsRefs != nil {
		m["ls-refs.pkt"] = fx.lsRefs
	}
	if fx.fetch != nil {
		m["fetch.pkt"] = fx.fetch
	}
	return m
}

// TestSyntheticFixturesCurrent holds testdata/synthetic equal to the
// helper's output (synthetic packs, in the wire format the recorded fixtures
// use).
func TestSyntheticFixturesCurrent(t *testing.T) {
	sets := map[string]map[string][]byte{}
	for name, fx := range syntheticFixtures() {
		sets[filepath.Join(syntheticDir, name)] = fixtureFiles(fx)
	}
	for name, files := range filterFixtures() {
		sets[filepath.Join(filterDir, name)] = files
	}
	for dir, files := range sets {
		for file, want := range files {
			p := filepath.Join(dir, file)
			if *update {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, want, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("%s: %v (run with -update)", p, err)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from the helper's output (run with -update and read the diff)", p)
			}
		}
	}
}
