// Why: a huge tree object or entry name must not make the tree walk use memory
// in proportion to it.
// What: TestHugeTree and TestHugeName.
// How: a 64 MiB tree object and a 64 MiB entry name cost memory bounded by the
// limits, measured by allocDuring, and their errors are short.

package git

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
)

// allocDuring is the bytes allocated while f runs.
func allocDuring(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// storeWithRoot is a Store holding one blob, the tree data and a commit
// naming it, with no pack (the pack's own costs are tested elsewhere).
func storeWithRoot(data []byte, blob []byte) (*Store, OID) {
	st := &Store{objs: map[OID]Object{HashObject(ObjBlob, blob): {Type: ObjBlob, Data: blob}}}
	root := HashObject(ObjTree, data)
	st.objs[root] = Object{Type: ObjTree, Data: data}
	commit := commitBytes(root, "c")
	c := HashObject(ObjCommit, commit)
	st.objs[c] = Object{Type: ObjCommit, Data: commit}
	return st, c
}

// TestHugeTree: a 64 MiB tree of 2.2 million entries stops at fetch's
// 100,000-entry limit having allocated a few MiB, not hundreds.
func TestHugeTree(t *testing.T) {
	blob := []byte("x")
	b := HashObject(ObjBlob, blob)
	var data bytes.Buffer
	for i := 0; data.Len() < 64<<20; i++ {
		fmt.Fprintf(&data, "100644 f%07d\x00", i)
		data.Write(b[:])
	}
	st, c := storeWithRoot(data.Bytes(), blob)
	var err error
	n := allocDuring(func() { _, err = st.Files(c, Limits{MaxFiles: 100_000}) })
	if !isReason(err, ReasonLimit) {
		t.Fatalf("a 64 MiB tree: %v", err)
	}
	if n > 48<<20 {
		t.Errorf("the walk allocated %d MiB for a 64 MiB tree under a 100,000-entry limit", n>>20)
	}
	t.Logf("64 MiB tree, 100,000-entry limit: %d KiB allocated (go test, busy PC, provisional)", n>>10)
}

// TestHugeName: a 64 MiB name is refused at 4,096 bytes, with no copy
// of it and a short message.
func TestHugeName(t *testing.T) {
	blob := []byte("x")
	b := HashObject(ObjBlob, blob)
	var data bytes.Buffer
	data.WriteString("100644 ")
	data.Write(bytes.Repeat([]byte{'n'}, 64<<20))
	data.WriteByte(0)
	data.Write(b[:])
	st, c := storeWithRoot(data.Bytes(), blob)
	var err error
	n := allocDuring(func() { _, err = st.Files(c, Limits{}) })
	if !isReason(err, ReasonCorrupt) {
		t.Fatalf("a 64 MiB name: %v", err)
	}
	if n > 1<<20 {
		t.Errorf("the walk allocated %d KiB for a 64 MiB name", n>>10)
	}
	if len(err.Error()) > 300 {
		t.Errorf("the message is %d bytes", len(err.Error()))
	}
	// A name of exactly 4,096 bytes is accepted; its message is capped.
	name := bytes.Repeat([]byte{'\x1b'}, MaxNameLen)
	data.Reset()
	data.WriteString("120000 ")
	data.Write(name)
	data.WriteByte(0)
	data.Write(b[:])
	st, c = storeWithRoot(data.Bytes(), blob)
	_, err = st.Files(c, Limits{})
	if !isReason(err, ReasonRefused) || len(err.Error()) > 1200 || bytes.ContainsRune([]byte(err.Error()), 0x1b) {
		t.Errorf("a 4,096-byte name of escapes: %d bytes, %.80q", len(err.Error()), err)
	}
}
