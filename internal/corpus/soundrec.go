// Why: a soundness violation or a miscompile must be kept as a file that
// reproduces it, with an id that does not change when the record is moved.
//
// What: RecordID, FoundRecord and MiscompileRecord with Encode, ReadFound,
// WriteFound, ReadMiscompile and WriteMiscompile, and recordViolation, which
// minimises a violating program and writes its record.
//
// How: records live under tests/soundness as Mk programs led by "// key:
// value" header lines; the id is the first 12 hex digits of the SHA-256 of
// the program text without them. A violation is recorded under found/open/
// with needs: unknown, and only the fix of its rule closes it.

package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"mk/internal/driver"
)

// RecordID is a record's id: the first 12 hex digits of the SHA-256
// of its program text with the leading // header lines and every trailing
// // <- Exxxx marker removed, so closing a record keeps its id.
func RecordID(text string) string {
	sum := sha256.Sum256([]byte(recordBody(text)))
	return hex.EncodeToString(sum[:])[:12]
}

var reMarker = regexp.MustCompile(`\s*// <- E[0-9]{4}(, E[0-9]{4})*\s*$`)

// recordBody is text without its leading // lines and its markers.
func recordBody(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "//") {
		i++
	}
	var b strings.Builder
	for _, l := range lines[i:] {
		b.WriteString(reMarker.ReplaceAllString(l, ""))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// FoundRecord is one violation or compiler-crash record.
type FoundRecord struct {
	ID        string
	Crash     bool   // a C record: the check crashed (sound: ice ...)
	Verdict   string // "ok", or the verdict the fixed compiler gives once closed
	Sound     string // the sound: line's text
	Needs     string // the rule it needs; "unknown" until triaged
	Gen       string // "1 full seed 0x... run 0x..."
	Found     string // "2026-11-07, run <label>, build <hex>"
	Minimised string // "87 lines to 14, 412 candidates"
	Closed    string // closing line, when closed
	Case      string // the H case added, when one was
	Kind      string // the violation kind, from Sound
	Seed, Run uint64
	Text      string // the minimised program
}

// MiscompileRecord is one oracle difference.
type MiscompileRecord struct {
	ID        string
	Verdict   string
	Oracle    string // "release differs; eval = dev"
	Owner     string
	Target    string
	Seeds     string // "k 0x.. eval 0x.. dev 0x.. release 0x.."
	Gen       string
	Found     string
	Minimised string
	Closed    string
	Text      string
}

func (r *FoundRecord) file() string {
	if r.Crash {
		return "C" + r.ID + ".mk"
	}
	return "F" + r.ID + ".mk"
}

// Encode spells the record as its .mk file.
func (r *FoundRecord) Encode() string {
	var b strings.Builder
	v := r.Verdict
	if v == "" {
		v = "ok"
	}
	fmt.Fprintf(&b, "// verdict: %s\n", v)
	fmt.Fprintf(&b, "// sound: %s\n", r.Sound)
	fmt.Fprintf(&b, "// needs: %s\n", orUnknown(r.Needs))
	fmt.Fprintf(&b, "// gen: %s\n", r.Gen)
	fmt.Fprintf(&b, "// found: %s\n", r.Found)
	fmt.Fprintf(&b, "// minimised: %s\n", r.Minimised)
	if r.Closed != "" {
		fmt.Fprintf(&b, "// closed: %s\n", r.Closed)
	}
	if r.Case != "" {
		fmt.Fprintf(&b, "// case: %s\n", r.Case)
	}
	b.WriteString(strings.TrimRight(r.Text, "\n"))
	b.WriteByte('\n')
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// recordHeader reads a record's leading // key: value lines.
func recordHeader(data []byte) (map[string]string, string) {
	h := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	i := 0
	for ; i < len(lines) && strings.HasPrefix(lines[i], "// "); i++ {
		k, v, ok := strings.Cut(strings.TrimPrefix(lines[i], "// "), ": ")
		if !ok {
			break
		}
		h[k] = v
	}
	return h, strings.Join(lines[i:], "\n")
}

var reGenSeeds = regexp.MustCompile(`seed (0x[0-9a-f]+)(?: run (0x[0-9a-f]+))?`)

// ReadFound parses one found record; path is its file name, checked against
// the id.
func ReadFound(path string, data []byte) (*FoundRecord, error) {
	h, text := recordHeader(data)
	r := &FoundRecord{Verdict: h["verdict"], Sound: h["sound"], Needs: h["needs"], Gen: h["gen"], Found: h["found"],
		Minimised: h["minimised"], Closed: h["closed"], Case: h["case"], Text: text}
	for _, k := range []string{"verdict", "sound", "needs", "gen", "found", "minimised"} {
		if _, ok := h[k]; !ok {
			return nil, fmt.Errorf("%s: no // %s: line", path, k)
		}
	}
	r.Crash = strings.HasPrefix(r.Sound, "ice")
	r.Kind, _, _ = strings.Cut(r.Sound, " ")
	if m := reGenSeeds.FindStringSubmatch(r.Gen); m != nil {
		fmt.Sscanf(m[1], "0x%x", &r.Seed)
		if m[2] != "" {
			fmt.Sscanf(m[2], "0x%x", &r.Run)
		}
	}
	r.ID = RecordID(string(data))
	if base := filepath.Base(path); base != r.file() {
		return nil, fmt.Errorf("%s: the file name is not the record's id (want %s)", path, r.file())
	}
	return r, nil
}

// WriteFound puts r under <root>/found/open/ and returns its path.
func WriteFound(root string, r *FoundRecord) (string, error) {
	r.ID = RecordID(r.Text)
	dir := filepath.Join(root, "found", "open")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, r.file())
	return p, os.WriteFile(p, []byte(r.Encode()), 0o644)
}

// Encode spells the record as its .mk file.
func (r *MiscompileRecord) Encode() string {
	var b strings.Builder
	v := r.Verdict
	if v == "" {
		v = "ok"
	}
	fmt.Fprintf(&b, "// verdict: %s\n", v)
	fmt.Fprintf(&b, "// oracle: %s\n", r.Oracle)
	fmt.Fprintf(&b, "// owner: %s\n", r.Owner)
	fmt.Fprintf(&b, "// target: %s\n", r.Target)
	fmt.Fprintf(&b, "// seed: %s\n", r.Seeds)
	fmt.Fprintf(&b, "// gen: %s\n", r.Gen)
	fmt.Fprintf(&b, "// found: %s\n", r.Found)
	fmt.Fprintf(&b, "// minimised: %s\n", r.Minimised)
	if r.Closed != "" {
		fmt.Fprintf(&b, "// closed: %s\n", r.Closed)
	}
	b.WriteString(strings.TrimRight(r.Text, "\n"))
	b.WriteByte('\n')
	return b.String()
}

// ReadMiscompile parses one miscompile record.
func ReadMiscompile(path string, data []byte) (*MiscompileRecord, error) {
	h, text := recordHeader(data)
	for _, k := range []string{"verdict", "oracle", "owner", "target", "seed", "gen", "found", "minimised"} {
		if _, ok := h[k]; !ok {
			return nil, fmt.Errorf("%s: no // %s: line", path, k)
		}
	}
	r := &MiscompileRecord{Verdict: h["verdict"], Oracle: h["oracle"], Owner: h["owner"], Target: h["target"], Seeds: h["seed"],
		Gen: h["gen"], Found: h["found"], Minimised: h["minimised"], Closed: h["closed"], Text: text}
	r.ID = RecordID(string(data))
	if base := filepath.Base(path); base != "M"+r.ID+".mk" {
		return nil, fmt.Errorf("%s: the file name is not the record's id (want M%s.mk)", path, r.ID)
	}
	return r, nil
}

// WriteMiscompile puts r under <root>/miscompile/open/ and returns its path.
func WriteMiscompile(root string, r *MiscompileRecord) (string, error) {
	r.ID = RecordID(r.Text)
	dir := filepath.Join(root, "miscompile", "open")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "M"+r.ID+".mk")
	return p, os.WriteFile(p, []byte(r.Encode()), 0o644)
}

// recordViolation minimises a violating program when cfg.Minimise is set
// and writes its found record under cfg.Out: keep(t) is
// "t is accepted, and the tagged run with the recorded run seed, then each
// of the program's other run seeds, reports a violation of the same kind".
// It returns the record's path, or "" when cfg.Out is empty.
func recordViolation(cfg SoundConfig, c SoundCompiler, p SoundProgram, k uint64, v SoundViolation) string {
	if cfg.Out == "" {
		return ""
	}
	text := p.Text
	stats := MinStats{FromLines: lineCount(text), ToLines: lineCount(text)}
	if cfg.Minimise {
		var n int64
		var mu sync.Mutex
		seeds := append([]uint64{k}, RunSeeds(p.Seed, cfg.Seeds)...)
		keep := func(t string) bool {
			mu.Lock()
			n++
			unit := fmt.Sprintf("sound/%s/min/%x/%d", cfg.Label, p.Seed, n)
			mu.Unlock()
			out := c.Check(unit, t)
			if cl, _ := classifySound(out.Report); cl != "accepted" {
				return false
			}
			for _, s := range seeds {
				r := c.RunUnit(SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{s}, Tag: &SoundTag{Seed: s, FailAlloc: soundFailAlloc(s)}})
				if len(r.Violations) > 0 && r.Violations[0].Kind == v.Kind {
					return true
				}
			}
			return false
		}
		text, stats = MinimiseStats(text, keep, MinBudget{Workers: min(cfg.Workers, 8)})
	}
	r := &FoundRecord{
		Sound:     v.Text,
		Gen:       fmt.Sprintf("%d %s seed %#016x run %#016x", GenVersion, cfg.Profile, p.Seed, k),
		Found:     fmt.Sprintf("%s, run %s, build %x", time.Now().Format("2006-01-02"), cfg.Label, driver.BuildID()),
		Minimised: fmt.Sprintf("%d lines to %d, %d candidates", stats.FromLines, stats.ToLines, stats.Candidates),
		Text:      text,
	}
	path, err := WriteFound(cfg.Out, r)
	if err != nil {
		return "error: " + err.Error()
	}
	return path
}
