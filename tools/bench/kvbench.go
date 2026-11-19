// Why: the kvserve row (tests/bench/app/kvserve/README.md) times key-value
// servers, and a fast server that answers or logs wrongly must fail, not win.
//
// What: tools/bench kv's load: kvLoad (the mix), kvSession (one connection
// speaking kvd's line protocol) and kvWalCheck, the check of the server's
// write-ahead log after it stops.
//
// How: each connection owns its own keys, so every reply is predictable
// from that connection's requests, and the log, replayed, must give exactly
// the store the connections' models hold; kvFold digests each connection's
// changes in order so the two can be compared.

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
)

// kvLoad is the kv generator's mix: the keys each connection owns, the
// percentages of GET, SET, INCR and DEL (which sum to 100), and the length
// of a SET's value. Keys with an even index hold text (SET), the others
// counters (INCR); GET and DEL take any key.
type kvLoad struct {
	Keys     int `json:"keys"`
	Get      int `json:"get"`
	Set      int `json:"set"`
	Incr     int `json:"incr"`
	Del      int `json:"del"`
	ValueMin int `json:"value_min"`
	ValueMax int `json:"value_max"`
}

func (l *kvLoad) check() error {
	if l.Keys < 2 || l.Keys%2 != 0 {
		return errors.New("keys must be even and at least 2: half text keys, half counters")
	}
	if l.Get < 0 || l.Set < 0 || l.Incr < 0 || l.Del < 0 || l.Get+l.Set+l.Incr+l.Del != 100 {
		return errors.New("get, set, incr and del are percentages summing to 100")
	}
	if l.ValueMin < 1 || l.ValueMax < l.ValueMin || l.ValueMax > 1024 {
		return errors.New("value_min and value_max: 1 <= min <= max <= 1024")
	}
	return nil
}

// kvValueBytes is a value's alphabet: letters, digits and the space, which
// kvd keeps inside a value (the value is the rest of the line); a value
// never starts or ends with the space, since kvd trims the line.
const kvValueBytes = "abcdefghijklmnopqrstuvwxyz0123456789 "

// kvSession is one connection's side of the kv load.
type kvSession struct {
	load  *kvLoad
	g     splitmix
	keys  []string
	model map[string]string
	// The request in flight: its line, its expected reply (without the
	// line break), and the model change once the reply is checked.
	line    []byte
	want    []byte
	key     string
	set     bool // the change sets key to val, else (when del) removes it
	del     bool
	val     string
	changes int64 // the changes kvd logs: SET, INCR, and DEL of a present key
	// digest folds every answered change in order (kvFold), so the log
	// check can compare each connection's changes with its keys' records,
	// in order.
	digest uint64
}

// newKVSession is connection i's session, drawn from splitmix(seed + i).
func newKVSession(s *serverSpec, i int) session {
	l := s.KV
	k := &kvSession{load: l, g: splitmix{s.Seed + uint64(i)*0x9E3779B97F4A7C15}, model: map[string]string{}, digest: kvFoldBasis}
	for j := 0; j < l.Keys; j++ {
		k.keys = append(k.keys, "c"+strconv.Itoa(i)+"k"+strconv.Itoa(j))
	}
	return k
}

func (k *kvSession) request() []byte {
	l := k.load
	p := int(k.g.next() % 100)
	j := int(k.g.next() % uint64(l.Keys))
	k.set, k.del = false, false
	k.line = k.line[:0]
	k.want = k.want[:0]
	switch {
	case p < l.Get:
		k.key = k.keys[j]
		k.line = append(append(k.line, "GET "...), k.key...)
		if v, ok := k.model[k.key]; ok {
			k.want = append(append(k.want, '$'), v...)
		} else {
			k.want = append(k.want, '_')
		}
	case p < l.Get+l.Set:
		k.key = k.keys[j&^1] // a text key
		n := l.ValueMin + int(k.g.next()%uint64(l.ValueMax-l.ValueMin+1))
		v := make([]byte, n)
		for x := range v {
			alpha := kvValueBytes
			if x == 0 || x == n-1 {
				alpha = kvValueBytes[:len(kvValueBytes)-1]
			}
			v[x] = alpha[k.g.next()%uint64(len(alpha))]
		}
		k.val, k.set = string(v), true
		k.line = append(append(append(append(k.line, "SET "...), k.key...), ' '), v...)
		k.want = append(k.want, "+OK"...)
	case p < l.Get+l.Set+l.Incr:
		k.key = k.keys[j|1] // a counter
		n := int64(0)
		if v, ok := k.model[k.key]; ok {
			n, _ = strconv.ParseInt(v, 10, 64)
		}
		k.val, k.set = strconv.FormatInt(n+1, 10), true
		k.line = append(append(k.line, "INCR "...), k.key...)
		k.want = append(append(k.want, ':'), k.val...)
	default:
		k.key = k.keys[j]
		k.line = append(append(k.line, "DEL "...), k.key...)
		if _, ok := k.model[k.key]; ok {
			k.del = true
			k.want = append(k.want, ":1"...)
		} else {
			k.want = append(k.want, ":0"...)
		}
	}
	k.line = append(k.line, '\n')
	return k.line
}

func (k *kvSession) check(br *bufio.Reader) error {
	line, err := br.ReadSlice('\n')
	if err != nil {
		return err
	}
	got := line[:len(line)-1]
	if !bytes.Equal(got, k.want) {
		return fmt.Errorf("%q answered %q, want %q", bytes.TrimSpace(k.line), got, k.want)
	}
	switch {
	case k.set:
		k.model[k.key] = k.val
		k.changes++
		k.digest = kvFold(k.digest, kvOpSet, k.key, k.val)
	case k.del:
		delete(k.model, k.key)
		k.changes++
		k.digest = kvFold(k.digest, kvOpDel, k.key, "")
	}
	return nil
}

// kvFoldBasis and kvFold are FNV-1a 64 over a change: its operation, its
// key and, for a set, its value, each length-prefixed, folded into h.
const kvFoldBasis = 0xcbf29ce484222325

func kvFold(h uint64, op byte, key, val string) uint64 {
	b := func(c byte) { h = (h ^ uint64(c)) * 0x100000001b3 }
	b(op)
	for _, s := range []string{key, val} {
		n := len(s)
		b(byte(n))
		b(byte(n >> 8))
		b(byte(n >> 16))
		for i := 0; i < n; i++ {
			b(s[i])
		}
	}
	return h
}

// kvd's log format (Dogfoods/kvd/src/wal/format.mk): every integer
// little-endian; a 24-byte header ("KVDWAL1\n", the generation u64, the
// CRC-32 of the first 16 bytes u32, the clean-shutdown flag u8, three zero
// bytes), then records (payload length u32, CRC-32 of the payload u32,
// payload: operation u8, key length u16, key, value). Operations: 1 set,
// 2 delete, 3 append.
const (
	kvWalMagic  = "KVDWAL1\n"
	kvWalHeader = 24
	kvOpSet     = 1
	kvOpDel     = 2
	kvOpAppend  = 3
)

// kvWalCheck reads the log a stopped server left in data and checks it:
// the header of a fresh log (generation 0, not shut down cleanly: the
// server was killed), every record whole and its CRC-32 right, one record
// per change the sessions were answered for, and the store the records
// replay to equal to the union of the sessions' models (each connection
// owns its keys, so the union is exact), and each connection's changes,
// in order, equal to the records of its keys in the log's order (the
// sessions' digests), so a wrong intermediate value or one connection's
// changes out of order fails too. The server is killed only after
// every connection has its last reply, and each change is written before
// it is answered, so no answered change can be missing.
func kvWalCheck(data string, ss []session) (string, error) {
	b, err := os.ReadFile(filepath.Join(data, "kvd.wal"))
	if err != nil {
		return "", fmt.Errorf("the log: %w", err)
	}
	if len(b) < kvWalHeader || string(b[:8]) != kvWalMagic {
		return "", errors.New("the log: no kvd header")
	}
	le := binary.LittleEndian
	if crc32.ChecksumIEEE(b[:16]) != le.Uint32(b[16:]) {
		return "", errors.New("the log: the header's CRC-32 is wrong")
	}
	if g := le.Uint64(b[8:]); g != 0 || b[20] != 0 || b[21] != 0 || b[22] != 0 || b[23] != 0 {
		return "", fmt.Errorf("the log: header of generation %d, flag %d: want a fresh log's (generation 0, not clean)", g, b[20])
	}
	owner := map[string]int{}
	for i, s := range ss {
		for _, key := range s.(*kvSession).keys {
			owner[key] = i
		}
	}
	digests := make([]uint64, len(ss))
	for i := range digests {
		digests[i] = kvFoldBasis
	}
	store := map[string]string{}
	var records int64
	for pos := kvWalHeader; pos < len(b); {
		if len(b)-pos < 11 {
			return "", fmt.Errorf("the log: a torn record at %d", pos)
		}
		n := int(le.Uint32(b[pos:]))
		sum := le.Uint32(b[pos+4:])
		if n < 3 || n > len(b)-pos-8 {
			return "", fmt.Errorf("the log: record at %d: length %d", pos, n)
		}
		p := b[pos+8 : pos+8+n]
		if crc32.ChecksumIEEE(p) != sum {
			return "", fmt.Errorf("the log: record at %d: CRC-32 %08x, stored %08x", pos, crc32.ChecksumIEEE(p), sum)
		}
		kl := int(le.Uint16(p[1:]))
		if kl > n-3 {
			return "", fmt.Errorf("the log: record at %d: key length %d", pos, kl)
		}
		key, val := string(p[3:3+kl]), string(p[3+kl:])
		i, ok := owner[key]
		if !ok {
			return "", fmt.Errorf("the log: record at %d: key %q, which no connection owns", pos, key)
		}
		switch p[0] {
		case kvOpSet:
			store[key] = val
			digests[i] = kvFold(digests[i], kvOpSet, key, val)
		case kvOpDel:
			delete(store, key)
			digests[i] = kvFold(digests[i], kvOpDel, key, "")
		case kvOpAppend:
			store[key] += val
			digests[i] = kvFold(digests[i], kvOpAppend, key, val)
		default:
			return "", fmt.Errorf("the log: record at %d: operation %d", pos, p[0])
		}
		records++
		pos += 8 + n
	}
	want := map[string]string{}
	var changes int64
	for _, s := range ss {
		k := s.(*kvSession)
		for key, v := range k.model {
			want[key] = v
		}
		changes += k.changes
	}
	if records != changes {
		return "", fmt.Errorf("the log holds %d records, and the server answered %d changes", records, changes)
	}
	if len(store) != len(want) {
		return "", fmt.Errorf("the log replays to %d keys, the connections hold %d", len(store), len(want))
	}
	for key, v := range want {
		if got, ok := store[key]; !ok || got != v {
			return "", fmt.Errorf("the log replays %s to %q, the connection was told %q", key, got, v)
		}
	}
	for i, s := range ss {
		if digests[i] != s.(*kvSession).digest {
			return "", fmt.Errorf("the log's records of connection %d's keys are not its answered changes in order", i)
		}
	}
	return fmt.Sprintf(", the log replayed (%d records, %d keys, %d bytes)", records, len(store), len(b)), nil
}
