// kvserve, Go, std: the task of ../README.md, the subset of kvd's line
// protocol the load uses (PING, GET, SET, INCR, DEL, QUIT) over TCP, every
// change appended to a write-ahead log in kvd's format before it is
// answered. Usage: kvserve serve --addr HOST:PORT --data DIR [--sync
// always|never|N]. Serves until killed. One goroutine per connection
// (net.Listener.Accept; GOMAXPROCS is the row's thread count, set by
// tools/bench), a 4 KiB bufio.Reader and bufio.Writer on each, the writer
// flushed after every reply. The store is a map[string]string behind a
// sync.RWMutex; the log is an *os.File opened to append behind a
// sync.Mutex, each record written whole by one Write and checked by
// hash/crc32's IEEE table. A change takes the log's lock, reads the store
// where it depends on it (DEL, INCR), appends its record, syncs it as the
// policy says, then takes the store's lock and applies it, as kvd's
// Change does, so the log's order is the store's.
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	maxKey    = 256
	maxValue  = 65536
	lineLimit = maxKey + maxValue + 64
	opSet     = 1
	opDel     = 2
)

type server struct {
	mu    sync.RWMutex // the store
	store map[string]string

	logMu   sync.Mutex // the log, its file and the changes since the last sync
	log     *os.File
	every   int // 0: never sync; 1: sync each change; n: every n changes
	pending int
	rec     []byte
}

func main() {
	addr, dir, every, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "kvserve:", err)
		os.Exit(2)
	}
	s := &server{store: map[string]string{}, every: every}
	if err := s.openLog(dir); err != nil {
		fmt.Fprintln(os.Stderr, "kvserve:", err)
		os.Exit(1)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kvserve: listen:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "kvserve: listening on", l.Addr())
	for {
		c, err := l.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, "kvserve: accept:", err)
			os.Exit(1)
		}
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		go s.serve(c)
	}
}

// parseArgs reads `serve --addr A --data D [--sync P]`.
func parseArgs(args []string) (addr, dir string, every int, err error) {
	if len(args) == 0 || args[0] != "serve" {
		return "", "", 0, errors.New("usage: kvserve serve --addr HOST:PORT --data DIR [--sync always|never|N]")
	}
	addr, dir, sync := "127.0.0.1:7379", "kvd-data", "always"
	for i := 1; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return "", "", 0, fmt.Errorf("%s needs a value", args[i])
		}
		switch args[i] {
		case "--addr":
			addr = args[i+1]
		case "--data":
			dir = args[i+1]
		case "--sync":
			sync = args[i+1]
		default:
			return "", "", 0, fmt.Errorf("unknown flag %s", args[i])
		}
	}
	switch sync {
	case "always":
		every = 1
	case "never":
		every = 0
	default:
		n, err := strconv.Atoi(sync)
		if err != nil || n < 1 || n > 1_000_000 {
			return "", "", 0, errors.New("--sync takes always, never or a number of changes")
		}
		every = n
	}
	return addr, dir, every, nil
}

// openLog makes dir and a fresh log in it: the 24-byte header of
// generation 0, not shut down cleanly, synced. Recovery is kvd's; a twin
// starts empty, so it refuses a directory that already holds a log.
func (s *server) openLog(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "kvd.wal"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	var h [24]byte
	copy(h[:], "KVDWAL1\n")
	binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:16]))
	if _, err := f.Write(h[:]); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	s.log = f
	return nil
}

// appendRecord writes one record (length, CRC-32 of the payload, payload:
// operation, key length, key, value) and syncs it as the policy says. The
// caller holds logMu.
func (s *server) appendRecord(op byte, key, value string) error {
	n := 3 + len(key) + len(value)
	r := append(s.rec[:0], make([]byte, 11)...)
	binary.LittleEndian.PutUint32(r[0:], uint32(n))
	r[8] = op
	binary.LittleEndian.PutUint16(r[9:], uint16(len(key)))
	r = append(append(r, key...), value...)
	binary.LittleEndian.PutUint32(r[4:], crc32.ChecksumIEEE(r[8:]))
	s.rec = r
	if _, err := s.log.Write(r); err != nil {
		return err
	}
	if s.every > 0 {
		s.pending++
		if s.pending >= s.every {
			s.pending = 0
			return s.log.Sync()
		}
	}
	return nil
}

// serve answers one connection's lines until it leaves.
func (s *server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 4096)
	w := bufio.NewWriterSize(c, 4096)
	var line []byte
	for {
		var err error
		line, err = readLine(r, line[:0])
		if err != nil {
			if errors.Is(err, errTooLong) {
				c.Write([]byte("-ERR LineTooLong\n"))
			}
			return
		}
		quit := s.answer(w, strings.TrimSpace(string(line)))
		if w.Flush() != nil || quit {
			return
		}
	}
}

var errTooLong = errors.New("line too long")

// readLine reads one line without its "\n" into buf, at most lineLimit
// bytes with it; a line cut off by the end of the stream is never run.
func readLine(r *bufio.Reader, buf []byte) ([]byte, error) {
	for {
		part, err := r.ReadSlice('\n')
		buf = append(buf, part...)
		if len(buf) > lineLimit {
			return nil, errTooLong
		}
		switch {
		case err == nil:
			return buf[:len(buf)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

// answer runs one request and writes its reply; it reports QUIT.
func (s *server) answer(w *bufio.Writer, text string) bool {
	word, rest, _ := strings.Cut(text, " ")
	switch word {
	case "":
		w.WriteString("-ERR Empty\n")
	case "PING", "ping":
		if rest != "" {
			w.WriteString("-ERR ExtraArgument\n")
			return false
		}
		w.WriteString("+PONG\n")
	case "GET", "get":
		key, ok := keyOnly(w, rest)
		if !ok {
			return false
		}
		s.mu.RLock()
		v, found := s.store[key]
		s.mu.RUnlock()
		if found {
			w.WriteString("$")
			w.WriteString(v)
			w.WriteString("\n")
		} else {
			w.WriteString("_\n")
		}
	case "SET", "set":
		key, value, _ := strings.Cut(rest, " ")
		if !checkKey(w, key) || !checkValue(w, value) {
			return false
		}
		s.change(w, opSet, key, value, func() { w.WriteString("+OK\n") })
	case "INCR", "incr":
		key, ok := keyOnly(w, rest)
		if !ok {
			return false
		}
		s.incr(w, key)
	case "DEL", "del":
		key, ok := keyOnly(w, rest)
		if !ok {
			return false
		}
		s.del(w, key)
	case "QUIT", "quit":
		if rest != "" {
			w.WriteString("-ERR ExtraArgument\n")
			return false
		}
		w.WriteString("+BYE\n")
		return true
	default:
		w.WriteString("-ERR UnknownCommand\n")
	}
	return false
}

// change logs and applies a SET of key to value under the log's lock,
// then the store's, and writes reply once it is applied.
func (s *server) change(w *bufio.Writer, op byte, key, value string, reply func()) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.applyLocked(w, op, key, value, reply)
}

// applyLocked is change with logMu held.
func (s *server) applyLocked(w *bufio.Writer, op byte, key, value string, reply func()) {
	if err := s.appendRecord(op, key, value); err != nil {
		fmt.Fprintf(w, "-ERR %v\n", err)
		return
	}
	s.mu.Lock()
	if op == opDel {
		delete(s.store, key)
	} else {
		s.store[key] = value
	}
	s.mu.Unlock()
	reply()
}

// incr is INCR: the value read as an integer (0 when missing), plus one,
// logged as a SET of the new value.
func (s *server) incr(w *bufio.Writer, key string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.mu.RLock()
	v, found := s.store[key]
	s.mu.RUnlock()
	n := int64(0)
	if found {
		var err error
		if n, err = strconv.ParseInt(v, 10, 64); err != nil {
			w.WriteString("-ERR NotAnInteger\n")
			return
		}
	}
	next := strconv.FormatInt(n+1, 10)
	s.applyLocked(w, opSet, key, next, func() {
		w.WriteString(":")
		w.WriteString(next)
		w.WriteString("\n")
	})
}

// del is DEL: a missing key is answered 0 and logs nothing.
func (s *server) del(w *bufio.Writer, key string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.mu.RLock()
	_, found := s.store[key]
	s.mu.RUnlock()
	if !found {
		w.WriteString(":0\n")
		return
	}
	s.applyLocked(w, opDel, key, "", func() { w.WriteString(":1\n") })
}

// keyOnly is a command's one key, with nothing after it.
func keyOnly(w *bufio.Writer, rest string) (string, bool) {
	key, extra, _ := strings.Cut(rest, " ")
	if extra != "" {
		w.WriteString("-ERR ExtraArgument\n")
		return "", false
	}
	return key, checkKey(w, key)
}

// checkKey: non-empty, at most maxKey bytes, no space or control byte.
func checkKey(w *bufio.Writer, key string) bool {
	if key == "" {
		w.WriteString("-ERR MissingArgument\n")
		return false
	}
	if len(key) > maxKey {
		fmt.Fprintf(w, "-ERR KeyTooLong(len: %d, limit: %d)\n", len(key), maxKey)
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] <= ' ' || key[i] == 0x7f {
			fmt.Fprintf(w, "-ERR BadByte(offset: %d)\n", i)
			return false
		}
	}
	return true
}

// checkValue: at most maxValue bytes, no line break.
func checkValue(w *bufio.Writer, value string) bool {
	if len(value) > maxValue {
		fmt.Fprintf(w, "-ERR ValueTooLong(len: %d, limit: %d)\n", len(value), maxValue)
		return false
	}
	if i := strings.IndexAny(value, "\r\n"); i >= 0 {
		fmt.Fprintf(w, "-ERR BadByte(offset: %d)\n", i)
		return false
	}
	return true
}
