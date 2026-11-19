// Why: RFC 8448's example handshakes are an independent oracle for std.tls's
// key schedule, record protection and message codecs.
// What: TestStdTls_RFC8448KeySchedule, TestStdTls_RFC8448RecordProtection and
// TestStdTls_RFC8448MessageRoundTrip over the 1-RTT and HelloRetryRequest
// traces.
// How: the traces are read from Mk-evidence/rfc/rfc8448.txt; Go's crypto/sha256
// recomputes each transcript hash the trace shows, so a misread trace fails.

package std

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tlsTraceStep is one {role} step of a trace with its labelled values.
type tlsTraceStep struct {
	role   string
	action string
	line   int
	fields []tlsTraceField
}

type tlsTraceField struct {
	name string
	b    []byte
}

func (s *tlsTraceStep) field(name string) []byte {
	for _, f := range s.fields {
		if f.name == name {
			return f.b
		}
	}
	return nil
}

func (s *tlsTraceStep) has(name string) bool {
	for _, f := range s.fields {
		if f.name == name {
			return true
		}
	}
	return false
}

var (
	tlsStepRe  = regexp.MustCompile(`^   \{(client|server)\}  (.*)$`)
	tlsFieldRe = regexp.MustCompile(`^      ([A-Za-z][A-Za-z ]*?) \((\d+) octets\):\s+(.*)$`)
	tlsZeroRe  = regexp.MustCompile(`^      ([A-Za-z][A-Za-z ]*?):\s+0 \(all zero octets\)`)
	tlsHexRe   = regexp.MustCompile(`^\s+([0-9a-f]{2}( [0-9a-f]{2})*)\s*$`)
)

// tlsEvidence finds Mk-evidence: $MK_EVIDENCE or the directory beside the
// repository.
func tlsEvidence(t testing.TB) string {
	t.Helper()
	if d := os.Getenv("MK_EVIDENCE"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "Mk-evidence")
}

// tlsTrace reads the steps of RFC 8448 between the section headings from
// and to (exclusive), for instance "3.  " and "4.  ".
func tlsTrace(t testing.TB, from, to string) []tlsTraceStep {
	t.Helper()
	path := filepath.Join(tlsEvidence(t), "rfc", "rfc8448.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("RFC 8448 is not in Mk-evidence (set MK_EVIDENCE): %v", err)
	}
	defer f.Close()
	var steps []tlsTraceStep
	in := false
	sc := bufio.NewScanner(f)
	n := 0
	var cur *tlsTraceStep
	var field *tlsTraceField
	want := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\f ")
		if strings.HasPrefix(line, from) {
			in = true
			continue
		}
		if strings.HasPrefix(line, to) && in {
			break
		}
		if !in || strings.HasPrefix(line, "Thomson") || strings.HasPrefix(line, "RFC 8448") {
			continue
		}
		if m := tlsStepRe.FindStringSubmatch(line); m != nil {
			steps = append(steps, tlsTraceStep{role: m[1], action: m[2], line: n})
			cur = &steps[len(steps)-1]
			field = nil
			continue
		}
		if cur == nil {
			continue
		}
		if field != nil && len(field.b) < want {
			if m := tlsHexRe.FindStringSubmatch(line); m != nil {
				b, err := hex.DecodeString(strings.ReplaceAll(m[1], " ", ""))
				if err != nil {
					t.Fatalf("rfc8448.txt:%d: %v", n, err)
				}
				field.b = append(field.b, b...)
				continue
			}
		}
		if m := tlsFieldRe.FindStringSubmatch(line); m != nil {
			k, _ := strconv.Atoi(m[2])
			cur.fields = append(cur.fields, tlsTraceField{name: m[1]})
			field = &cur.fields[len(cur.fields)-1]
			want = k
			rest := strings.TrimSpace(m[3])
			if rest != "(empty)" && rest != "" {
				b, err := hex.DecodeString(strings.ReplaceAll(rest, " ", ""))
				if err != nil {
					t.Fatalf("rfc8448.txt:%d: %v", n, err)
				}
				field.b = b
			}
			if field.b == nil {
				field.b = []byte{}
			}
			continue
		}
		if m := tlsZeroRe.FindStringSubmatch(line); m != nil {
			cur.fields = append(cur.fields, tlsTraceField{name: m[1], b: []byte{}})
			field = nil
			continue
		}
		if len(cur.fields) == 0 && strings.TrimSpace(line) != "" && !strings.HasSuffix(cur.action, ":") {
			cur.action += " " + strings.TrimSpace(line)
		}
	}
	for _, s := range steps {
		for _, f := range s.fields {
			if strings.HasSuffix(f.name, "octets") {
				t.Fatalf("rfc8448.txt:%d: misread field %q", s.line, f.name)
			}
		}
	}
	if len(steps) == 0 {
		t.Fatalf("no steps between %q and %q", from, to)
	}
	return steps
}

// tlsSections are the two traces std.tls is judged by.
var tlsSections = []struct{ name, from, to string }{
	{"1-RTT", "3.  Simple 1-RTT Handshake", "4.  Resumed 0-RTT Handshake"},
	{"HelloRetryRequest", "5.  HelloRetryRequest", "6.  Client Authentication"},
}

// tlsLabel is the quoted label of a step without "tls13 ".
func tlsLabel(action string) string {
	i := strings.Index(action, `"`)
	j := strings.LastIndex(action, `"`)
	if i < 0 || j <= i {
		return ""
	}
	return strings.TrimPrefix(action[i+1:j], "tls13 ")
}

// tlsMessages is each handshake message a trace constructs, in order, and
// the index of the step after which each was constructed.
func tlsMessages(steps []tlsTraceStep) (msgs [][]byte, at []int) {
	re := regexp.MustCompile(`^construct an? (\w+) handshake message:$`)
	for i, s := range steps {
		m := re.FindStringSubmatch(s.action)
		if m == nil || len(s.fields) == 0 {
			continue
		}
		msgs = append(msgs, s.fields[0].b)
		at = append(at, i)
	}
	return msgs, at
}

// tlsTranscriptOps is the "t" operations for the messages constructed
// before step i, with the restart after a HelloRetryRequest, and Go's
// SHA-256 over the same transcript.
func tlsTranscriptOps(msgs [][]byte, at []int, i int) (string, []byte) {
	var ops []any
	var goMsgs [][]byte
	for k, m := range msgs {
		if at[k] >= i {
			break
		}
		if m[0] == 2 && bytes.Equal(m[6:38], tlsHrrRandom) {
			ops = append(ops, "c", "r")
			d := sha256.Sum256(bytes.Join(goMsgs, nil))
			goMsgs = [][]byte{append([]byte{254, 0, 0, 32}, d[:]...)}
		}
		ops = append(ops, m)
		goMsgs = append(goMsgs, m)
	}
	d := sha256.Sum256(bytes.Join(goMsgs, nil))
	return tlsOps(ops...), d[:]
}

var tlsHrrRandom, _ = hex.DecodeString("cf21ad74e59a6111be1d8c021e65b891c2a211167abb8c5e079e09e2c8a8339c")

// tlsSuite is the cipher suite both traces use: TLS_AES_128_GCM_SHA256.
const tlsSuite = 0x1301

// TestStdTls_RFC8448KeySchedule checks (a): every extract and derive step
// with its values, both Finished values over the transcript, each
// transcript hash the trace shows, the traffic keys and IVs, and the whole
// schedule from the (EC)DHE secret.
func TestStdTls_RFC8448KeySchedule(t *testing.T) { tlsRun(t, tlsKeyScheduleCases(t)) }

// tlsKeyScheduleCases reads the cases of TestStdTls_RFC8448KeySchedule.
func tlsKeyScheduleCases(t *testing.T) []tlsCase {
	var cases []tlsCase
	for _, sec := range tlsSections {
		steps := tlsTrace(t, sec.from, sec.to)
		msgs, at := tlsMessages(steps)
		var early, hs, master []byte
		var shared []byte
		for i, s := range steps {
			name := fmt.Sprintf("%s rfc8448.txt:%d %s %s", sec.name, s.line, s.role, s.action)
			switch {
			case strings.HasPrefix(s.action, "extract secret") && s.has("secret"):
				cases = append(cases, tlsCase{name, fmt.Sprintf("x 256 %s %s", tlsHex(s.field("salt")), tlsHex(s.field("IKM"))), "ok " + tlsHex(s.field("secret"))})
				switch tlsLabel(s.action) {
				case "early":
					early = s.field("secret")
				case "handshake":
					hs = s.field("secret")
					shared = s.field("IKM")
				case "master":
					master = s.field("secret")
				}
			case strings.HasPrefix(s.action, "derive secret") && s.has("expanded"):
				label := tlsLabel(s.action)
				h := s.field("hash")
				// The trace's info is HkdfLabel; check it is what the label
				// and hash make, so the record below asks for what the trace
				// computed.
				info := []byte{0, byte(len(s.field("expanded"))), byte(6 + len(label))}
				info = append(info, "tls13 "+label...)
				info = append(info, byte(len(h)))
				info = append(info, h...)
				if !bytes.Equal(info, s.field("info")) {
					t.Fatalf("%s: HkdfLabel %x, trace %x", name, info, s.field("info"))
				}
				cases = append(cases, tlsCase{name, fmt.Sprintf("l 256 %s %s %s %d", tlsHex(s.field("PRK")), tlsHex([]byte(label)), tlsHex(h), len(s.field("expanded"))), "ok " + tlsHex(s.field("expanded"))})
				if label != "derived" {
					ops, goHash := tlsTranscriptOps(msgs, at, i)
					if !bytes.Equal(goHash, h) {
						t.Fatalf("%s: Go's transcript hash %x, trace %x", name, goHash, h)
					}
					cases = append(cases, tlsCase{name + " (transcript)", "t 256 " + ops, "ok " + tlsHex(h)})
				}
			case strings.Contains(s.action, "traffic keys") && s.has("key expanded"):
				cases = append(cases, tlsCase{name, fmt.Sprintf("k %d %s", tlsSuite, tlsHex(s.field("PRK"))), "ok " + tlsHex(s.field("key expanded")) + " " + tlsHex(s.field("iv expanded"))})
			case strings.HasPrefix(s.action, "calculate finished") && s.has("finished"):
				cases = append(cases, tlsCase{name + " (finished key)", fmt.Sprintf("l 256 %s %s  32", tlsHex(s.field("PRK")), tlsHex([]byte("finished"))), "ok " + tlsHex(s.field("expanded"))})
				ops, goHash := tlsTranscriptOps(msgs, at, i)
				cases = append(cases, tlsCase{name + " (verify_data)", fmt.Sprintf("f 256 %s %s", tlsHex(s.field("PRK")), tlsHex(goHash)), "ok " + tlsHex(s.field("finished"))})
				cases = append(cases, tlsCase{name + " (transcript)", "t 256 " + ops, "ok " + tlsHex(goHash)})
			}
		}
		if early == nil || hs == nil || master == nil {
			t.Fatalf("%s: the trace's early, handshake or master secret is missing", sec.name)
		}
		cases = append(cases, tlsCase{sec.name + " whole schedule", fmt.Sprintf("c 256 %s", tlsHex(shared)), "ok " + tlsHex(early) + tlsHex(hs) + tlsHex(master)})
		if sec.name == "1-RTT" {
			var cpriv, spub []byte
			for _, s := range steps {
				if strings.HasPrefix(s.action, "create an ephemeral x25519") {
					if s.role == "client" {
						cpriv = s.field("private key")
					} else {
						spub = s.field("public key")
					}
				}
			}
			cases = append(cases, tlsCase{"1-RTT X25519 shared secret", fmt.Sprintf("d %s %s", tlsHex(cpriv), tlsHex(spub)), "ok " + tlsHex(shared)})
		}
	}
	if len(cases) < 40 {
		t.Fatalf("only %d cases read from the traces", len(cases))
	}
	return cases
}

// tlsRecordType is the content type a "send ... record" step carries.
func tlsRecordType(action string) int {
	switch {
	case strings.Contains(action, "handshake record"):
		return 22
	case strings.Contains(action, "application_data record"):
		return 23
	case strings.Contains(action, "alert record"):
		return 21
	}
	return 0
}

// TestStdTls_RFC8448RecordProtection checks (b): each protected record of
// the two traces is sealed from its payload, as record number seq under
// the sender's current write key, to the trace's bytes, and opened back to
// its type and payload.
func TestStdTls_RFC8448RecordProtection(t *testing.T) { tlsRun(t, tlsRecordCases(t)) }

// tlsRecordCases reads the cases of TestStdTls_RFC8448RecordProtection.
func tlsRecordCases(t *testing.T) []tlsCase {
	var cases []tlsCase
	phaseRe := regexp.MustCompile(`traffic keys for (handshake|application) data`)
	for _, sec := range tlsSections {
		steps := tlsTrace(t, sec.from, sec.to)
		type key struct{ k, iv []byte }
		keys := map[string]key{}
		// First pass: every key and IV the trace shows, by writer and
		// phase; a "read" step gives the other side's write key.
		for _, s := range steps {
			m := phaseRe.FindStringSubmatch(s.action)
			if m == nil || !s.has("key expanded") {
				continue
			}
			writer := s.role
			if strings.HasPrefix(s.action, "derive read traffic") {
				writer = map[string]string{"client": "server", "server": "client"}[s.role]
			}
			keys[writer+" "+m[1]] = key{s.field("key expanded"), s.field("iv expanded")}
		}
		phase := map[string]string{}
		seq := map[string]int{}
		for _, s := range steps {
			if m := phaseRe.FindStringSubmatch(s.action); m != nil && strings.HasPrefix(s.action, "derive write traffic") {
				phase[s.role] = m[1]
				seq[s.role] = 0
				continue
			}
			ty := tlsRecordType(s.action)
			rec := s.field("complete record")
			if ty == 0 || rec == nil || rec[0] != 23 {
				continue
			}
			k, ok := keys[s.role+" "+phase[s.role]]
			if !ok {
				t.Fatalf("%s rfc8448.txt:%d: no key for %s %s", sec.name, s.line, s.role, phase[s.role])
			}
			name := fmt.Sprintf("%s rfc8448.txt:%d %s %s (%s, record %d)", sec.name, s.line, s.role, s.action, phase[s.role], seq[s.role])
			payload := s.field("payload")
			cases = append(cases, tlsCase{name + " seal", fmt.Sprintf("s %d %s %s %d %d %s", tlsSuite, tlsHex(k.k), tlsHex(k.iv), seq[s.role], ty, tlsHex(payload)), "ok " + tlsHex(rec)})
			cases = append(cases, tlsCase{name + " open", fmt.Sprintf("o %d %s %s %d %s", tlsSuite, tlsHex(k.k), tlsHex(k.iv), seq[s.role], tlsHex(rec)), fmt.Sprintf("ok %d %s", ty, tlsHex(payload))})
			// A flipped tag bit does not open: bad_record_mac (20).
			bad := bytes.Clone(rec)
			bad[len(bad)-1] ^= 1
			cases = append(cases, tlsCase{name + " forged", fmt.Sprintf("o %d %s %s %d %s", tlsSuite, tlsHex(k.k), tlsHex(k.iv), seq[s.role], tlsHex(bad)), "err alert 20"})
			// The wrong sequence number does not open either.
			cases = append(cases, tlsCase{name + " replayed", fmt.Sprintf("o %d %s %s %d %s", tlsSuite, tlsHex(k.k), tlsHex(k.iv), seq[s.role]+1, tlsHex(rec)), "err alert 20"})
			seq[s.role]++
		}
	}
	if len(cases) < 4*8 {
		t.Fatalf("only %d record cases read from the traces", len(cases))
	}
	return cases
}

// TestStdTls_RFC8448MessageRoundTrip checks (c): each handshake message
// the two traces construct, and each one inside a plaintext or protected
// record the traces send, parsed by std.tls's parser for its structure and
// written back by its writer, byte for byte.
func TestStdTls_RFC8448MessageRoundTrip(t *testing.T) { tlsRun(t, tlsMessageCases(t)) }

// tlsMessageCases reads the cases of TestStdTls_RFC8448MessageRoundTrip.
func tlsMessageCases(t *testing.T) []tlsCase {
	var cases []tlsCase
	seen := map[string]bool{}
	for _, sec := range tlsSections {
		steps := tlsTrace(t, sec.from, sec.to)
		var add func(name string, b []byte)
		add = func(name string, b []byte) {
			for len(b) >= 4 {
				n := 4 + (int(b[1])<<16 | int(b[2])<<8 | int(b[3]))
				if n > len(b) {
					t.Fatalf("%s: a message runs past its payload", name)
				}
				m := b[:n]
				if !seen[string(m)] {
					seen[string(m)] = true
					cases = append(cases, tlsCase{fmt.Sprintf("%s type %d", name, m[0]), "p " + tlsHex(m), "ok " + tlsHex(m)})
				}
				b = b[n:]
			}
		}
		for _, s := range steps {
			name := fmt.Sprintf("%s rfc8448.txt:%d %s", sec.name, s.line, s.action)
			if strings.HasPrefix(s.action, "construct a") && len(s.fields) > 0 {
				add(name, s.fields[0].b)
			}
			if tlsRecordType(s.action) == 22 && s.has("payload") {
				add(name, s.field("payload"))
			}
		}
	}
	types := map[byte]bool{}
	for _, c := range cases {
		b, _ := hex.DecodeString(strings.TrimPrefix(c.record, "p "))
		types[b[0]] = true
	}
	// ClientHello, ServerHello (and HelloRetryRequest), EncryptedExtensions,
	// Certificate, CertificateVerify, Finished, NewSessionTicket.
	for _, ty := range []byte{1, 2, 4, 8, 11, 15, 20} {
		if !types[ty] {
			t.Fatalf("the traces gave no message of type %d", ty)
		}
	}
	return cases
}
