// Why: std.encoding.cjk's tests need an oracle that shares nothing with the module but the index files.
// What: a Go model of the WHATWG Encoding Standard's CJK decoders and encoders.
// How: written from the standard's handlers, byte at a time over an I/O queue that a handler may push
// bytes or code points back onto; its pointer maps are built here from the standard's rules.

package std

import (
	"sync"
	"testing"
)

// cjkModel is the model over the pinned indexes.
type cjkModel struct {
	x *cjkIndexes
	// The index pointer of each encoder: the first pointer of a code
	// point, over the pointers each encoder's rule keeps.
	jis0208, sjis, euckr, gb18030, big5 map[rune]int
	pairs                               map[int][2]rune
}

var cjkModelOnce struct {
	sync.Once
	m   *cjkModel
	err error
}

func cjkModelGet(t testing.TB) *cjkModel {
	t.Helper()
	dir := cjkDir(t)
	cjkModelOnce.Do(func() {
		x, err := cjkLoad(dir)
		if err != nil {
			cjkModelOnce.err = err
			return
		}
		first := func(idx []int32, keep func(int) bool) map[rune]int {
			m := map[rune]int{}
			for p := 0; p < len(idx); p++ {
				if idx[p] < 0 || !keep(p) {
					continue
				}
				if _, seen := m[rune(idx[p])]; !seen {
					m[rune(idx[p])] = p
				}
			}
			return m
		}
		m := &cjkModel{x: x, pairs: map[int][2]rune{}}
		m.jis0208 = first(x.jis0208, func(int) bool { return true })
		m.sjis = first(x.jis0208, func(p int) bool { return p < 8272 || p > 8835 })
		m.euckr = first(x.euckr, func(int) bool { return true })
		m.gb18030 = first(x.gb18030, func(int) bool { return true })
		m.big5 = first(x.big5, func(p int) bool { return p >= (0xA1-0x81)*157 })
		// Big5: the last pointer for these six.
		for _, cp := range []rune{0x2550, 0x255E, 0x2561, 0x256A, 0x5341, 0x5345} {
			for p := (0xA1 - 0x81) * 157; p < len(x.big5); p++ {
				if rune(x.big5[p]) == cp {
					m.big5[cp] = p
				}
			}
		}
		for _, p := range x.pairs {
			m.pairs[int(p[0])] = [2]rune{rune(p[1]), rune(p[2])}
		}
		cjkModelOnce.m = m
	})
	if cjkModelOnce.err != nil {
		t.Fatal(cjkModelOnce.err)
	}
	return cjkModelOnce.m
}

// cjkCode is the code point at pointer p of idx, -1 for null.
func cjkCode(idx []int32, p int) rune {
	if p < 0 || p >= len(idx) {
		return -1
	}
	return rune(idx[p])
}

// The decoder handler results.
const (
	mFinished = iota
	mContinue
	mError
	mOut
)

// mItem is one item of the I/O queue: a byte and its offset, or
// end-of-queue (b -1).
type mItem struct{ b, at int }

// cjkDecoder is one decoder's handler and whether it is between units (no
// byte held), where a new unit starts.
type cjkDecoder interface {
	handle(it mItem) (kind int, out []rune, restore []mItem)
	idle() bool
}

// decode runs the standard's decode over b for name: the text, and the
// offset of the first malformed unit's first byte (-1 for none).
func (m *cjkModel) decode(name string, b []byte) (string, int) {
	d := m.decoder(name)
	queue := make([]mItem, 0, len(b)+1)
	for i, c := range b {
		queue = append(queue, mItem{int(c), i})
	}
	queue = append(queue, mItem{-1, len(b)})
	var out []rune
	bad, start := -1, 0
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.b < 0 {
			// End-of-queue stays: reading past it reads it again.
			queue = append(queue, it)
		}
		if d.idle() {
			start = it.at
		}
		kind, cps, restore := d.handle(it)
		if len(restore) > 0 {
			queue = append(append([]mItem{}, restore...), queue...)
		}
		switch kind {
		case mFinished:
			return string(out), bad
		case mError:
			out = append(out, 0xFFFD)
			if bad < 0 {
				bad = start
			}
			// A trail byte that opens an escape (ISO-2022-JP) starts the
			// next unit; elsewhere the next byte read idle does.
			if j, ok := d.(*mIso2022Jp); ok && j.state == jpEscStart {
				start = it.at
			}
		case mOut:
			out = append(out, cps...)
		}
	}
	return string(out), bad
}

func (m *cjkModel) decoder(name string) cjkDecoder {
	switch name {
	case "gb18030", "GBK":
		return &mGb18030{m: m}
	case "Big5":
		return &mBig5{m: m}
	case "EUC-JP":
		return &mEucJp{m: m}
	case "ISO-2022-JP":
		return &mIso2022Jp{m: m}
	case "Shift_JIS":
		return &mShiftJis{m: m}
	case "EUC-KR":
		return &mEucKr{m: m}
	}
	panic(name)
}

func isASCII(b int) bool { return b >= 0 && b <= 0x7F }

type mGb18030 struct {
	m                    *cjkModel
	first, second, third mItem
}

func (d *mGb18030) idle() bool { return d.first.b == 0 }

func (d *mGb18030) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	if b < 0 {
		if d.first.b == 0 && d.second.b == 0 && d.third.b == 0 {
			return mFinished, nil, nil
		}
		d.first.b, d.second.b, d.third.b = 0, 0, 0
		return mError, nil, nil
	}
	if d.third.b != 0 {
		if b < 0x30 || b > 0x39 {
			r := []mItem{d.second, d.third, it}
			d.first.b, d.second.b, d.third.b = 0, 0, 0
			return mError, nil, r
		}
		p := (d.first.b-0x81)*(10*126*10) + (d.second.b-0x30)*(10*126) + (d.third.b-0x81)*10 + b - 0x30
		d.first.b, d.second.b, d.third.b = 0, 0, 0
		cp := d.m.rangesCode(p)
		if cp < 0 {
			return mError, nil, nil
		}
		return mOut, []rune{cp}, nil
	}
	if d.second.b != 0 {
		if b >= 0x81 && b <= 0xFE {
			d.third = it
			return mContinue, nil, nil
		}
		r := []mItem{d.second, it}
		d.first.b, d.second.b = 0, 0
		return mError, nil, r
	}
	if d.first.b != 0 {
		if b >= 0x30 && b <= 0x39 {
			d.second = it
			return mContinue, nil, nil
		}
		lead := d.first.b
		d.first.b = 0
		p := -1
		off := 0x41
		if b < 0x7F {
			off = 0x40
		}
		if (b >= 0x40 && b <= 0x7E) || (b >= 0x80 && b <= 0xFE) {
			p = (lead-0x81)*190 + (b - off)
		}
		if cp := cjkCode(d.m.x.gb18030, p); cp >= 0 {
			return mOut, []rune{cp}, nil
		}
		if isASCII(b) {
			return mError, nil, []mItem{it}
		}
		return mError, nil, nil
	}
	if isASCII(b) {
		return mOut, []rune{rune(b)}, nil
	}
	if b == 0x80 {
		return mOut, []rune{0x20AC}, nil
	}
	if b >= 0x81 && b <= 0xFE {
		d.first = it
		return mContinue, nil, nil
	}
	return mError, nil, nil
}

// rangesCode is index gb18030 ranges code point, -1 for null.
func (m *cjkModel) rangesCode(p int) rune {
	if (p > 39419 && p < 189000) || p > 1237575 {
		return -1
	}
	if p == 7457 {
		return 0xE7C7
	}
	var off, cpOff int32
	for _, r := range m.x.ranges {
		if int(r[0]) <= p {
			off, cpOff = r[0], r[1]
		}
	}
	return rune(cpOff) + rune(p) - rune(off)
}

// rangesPointer is index gb18030 ranges pointer.
func (m *cjkModel) rangesPointer(cp rune) int {
	if cp == 0xE7C7 {
		return 7457
	}
	var off, pOff int32
	for _, r := range m.x.ranges {
		if rune(r[1]) <= cp {
			off, pOff = r[1], r[0]
		}
	}
	return int(pOff) + int(cp) - int(off)
}

type mBig5 struct {
	m    *cjkModel
	lead mItem
}

func (d *mBig5) idle() bool { return d.lead.b == 0 }

func (d *mBig5) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	if b < 0 && d.lead.b != 0 {
		d.lead.b = 0
		return mError, nil, nil
	}
	if b < 0 {
		return mFinished, nil, nil
	}
	if d.lead.b != 0 {
		lead := d.lead.b
		d.lead.b = 0
		p := -1
		off := 0x62
		if b < 0x7F {
			off = 0x40
		}
		if (b >= 0x40 && b <= 0x7E) || (b >= 0xA1 && b <= 0xFE) {
			p = (lead-0x81)*157 + (b - off)
		}
		if pr, ok := d.m.pairs[p]; ok {
			return mOut, []rune{pr[0], pr[1]}, nil
		}
		if cp := cjkCode(d.m.x.big5, p); cp >= 0 {
			return mOut, []rune{cp}, nil
		}
		if isASCII(b) {
			return mError, nil, []mItem{it}
		}
		return mError, nil, nil
	}
	if isASCII(b) {
		return mOut, []rune{rune(b)}, nil
	}
	if b >= 0x81 && b <= 0xFE {
		d.lead = it
		return mContinue, nil, nil
	}
	return mError, nil, nil
}

type mEucJp struct {
	m       *cjkModel
	jis0212 bool
	lead    mItem
}

func (d *mEucJp) idle() bool { return d.lead.b == 0 }

func (d *mEucJp) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	if b < 0 && d.lead.b != 0 {
		d.lead.b = 0
		return mError, nil, nil
	}
	if b < 0 {
		return mFinished, nil, nil
	}
	if d.lead.b == 0x8E && b >= 0xA1 && b <= 0xDF {
		d.lead.b = 0
		return mOut, []rune{rune(0xFF61 - 0xA1 + b)}, nil
	}
	if d.lead.b == 0x8F && b >= 0xA1 && b <= 0xFE {
		d.jis0212 = true
		d.lead = it
		return mContinue, nil, nil
	}
	if d.lead.b != 0 {
		lead := d.lead.b
		d.lead.b = 0
		cp := rune(-1)
		if lead >= 0xA1 && lead <= 0xFE && b >= 0xA1 && b <= 0xFE {
			idx := d.m.x.jis0208
			if d.jis0212 {
				idx = d.m.x.jis0212
			}
			cp = cjkCode(idx, (lead-0xA1)*94+b-0xA1)
		}
		d.jis0212 = false
		if cp >= 0 {
			return mOut, []rune{cp}, nil
		}
		if isASCII(b) {
			return mError, nil, []mItem{it}
		}
		return mError, nil, nil
	}
	if isASCII(b) {
		return mOut, []rune{rune(b)}, nil
	}
	if b == 0x8E || b == 0x8F || (b >= 0xA1 && b <= 0xFE) {
		d.lead = it
		return mContinue, nil, nil
	}
	return mError, nil, nil
}

// ISO-2022-JP decoder states.
const (
	jpASCII = iota
	jpRoman
	jpKatakana
	jpLeadByte
	jpTrailByte
	jpEscStart
	jpEscape
)

type mIso2022Jp struct {
	m                  *cjkModel
	state, outputState int
	lead               mItem
	output             bool
}

func (d *mIso2022Jp) idle() bool {
	return d.state == jpASCII || d.state == jpRoman || d.state == jpKatakana || d.state == jpLeadByte
}

func (d *mIso2022Jp) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	switch d.state {
	case jpASCII, jpRoman, jpKatakana, jpLeadByte:
		if b == 0x1B {
			d.state = jpEscStart
			return mContinue, nil, nil
		}
		if b < 0 {
			return mFinished, nil, nil
		}
		d.output = false
		switch d.state {
		case jpASCII:
			if b <= 0x7F && b != 0x0E && b != 0x0F {
				return mOut, []rune{rune(b)}, nil
			}
		case jpRoman:
			if b == 0x5C {
				return mOut, []rune{0xA5}, nil
			}
			if b == 0x7E {
				return mOut, []rune{0x203E}, nil
			}
			if b <= 0x7F && b != 0x0E && b != 0x0F {
				return mOut, []rune{rune(b)}, nil
			}
		case jpKatakana:
			if b >= 0x21 && b <= 0x5F {
				return mOut, []rune{rune(0xFF61 - 0x21 + b)}, nil
			}
		case jpLeadByte:
			if b >= 0x21 && b <= 0x7E {
				d.lead = it
				d.state = jpTrailByte
				return mContinue, nil, nil
			}
		}
		return mError, nil, nil
	case jpTrailByte:
		if b == 0x1B {
			d.state = jpEscStart
			return mError, nil, nil
		}
		d.state = jpLeadByte
		if b >= 0x21 && b <= 0x7E {
			cp := cjkCode(d.m.x.jis0208, (d.lead.b-0x21)*94+b-0x21)
			if cp < 0 {
				return mError, nil, nil
			}
			return mOut, []rune{cp}, nil
		}
		return mError, nil, nil
	case jpEscStart:
		if b == 0x24 || b == 0x28 {
			d.lead = it
			d.state = jpEscape
			return mContinue, nil, nil
		}
		var r []mItem
		if b >= 0 {
			r = []mItem{it}
		}
		d.output = false
		d.state = d.outputState
		return mError, nil, r
	case jpEscape:
		lead := d.lead
		d.lead.b = 0
		st := -1
		switch {
		case lead.b == 0x28 && b == 0x42:
			st = jpASCII
		case lead.b == 0x28 && b == 0x4A:
			st = jpRoman
		case lead.b == 0x28 && b == 0x49:
			st = jpKatakana
		case lead.b == 0x24 && (b == 0x40 || b == 0x42):
			st = jpLeadByte
		}
		if st >= 0 {
			d.state, d.outputState = st, st
			out := d.output
			d.output = true
			if out {
				return mError, nil, nil
			}
			return mContinue, nil, nil
		}
		r := []mItem{lead}
		if b >= 0 {
			r = append(r, it)
		}
		d.output = false
		d.state = d.outputState
		return mError, nil, r
	}
	panic("state")
}

type mShiftJis struct {
	m    *cjkModel
	lead mItem
}

func (d *mShiftJis) idle() bool { return d.lead.b == 0 }

func (d *mShiftJis) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	if b < 0 && d.lead.b != 0 {
		d.lead.b = 0
		return mError, nil, nil
	}
	if b < 0 {
		return mFinished, nil, nil
	}
	if d.lead.b != 0 {
		lead := d.lead.b
		d.lead.b = 0
		p := -1
		off := 0x41
		if b < 0x7F {
			off = 0x40
		}
		loff := 0xC1
		if lead < 0xA0 {
			loff = 0x81
		}
		if (b >= 0x40 && b <= 0x7E) || (b >= 0x80 && b <= 0xFC) {
			p = (lead-loff)*188 + b - off
		}
		if p >= 8836 && p <= 10715 {
			return mOut, []rune{rune(0xE000 - 8836 + p)}, nil
		}
		if cp := cjkCode(d.m.x.jis0208, p); cp >= 0 {
			return mOut, []rune{cp}, nil
		}
		if isASCII(b) {
			return mError, nil, []mItem{it}
		}
		return mError, nil, nil
	}
	if isASCII(b) || b == 0x80 {
		return mOut, []rune{rune(b)}, nil
	}
	if b >= 0xA1 && b <= 0xDF {
		return mOut, []rune{rune(0xFF61 - 0xA1 + b)}, nil
	}
	if (b >= 0x81 && b <= 0x9F) || (b >= 0xE0 && b <= 0xFC) {
		d.lead = it
		return mContinue, nil, nil
	}
	return mError, nil, nil
}

type mEucKr struct {
	m    *cjkModel
	lead mItem
}

func (d *mEucKr) idle() bool { return d.lead.b == 0 }

func (d *mEucKr) handle(it mItem) (int, []rune, []mItem) {
	b := it.b
	if b < 0 && d.lead.b != 0 {
		d.lead.b = 0
		return mError, nil, nil
	}
	if b < 0 {
		return mFinished, nil, nil
	}
	if d.lead.b != 0 {
		lead := d.lead.b
		d.lead.b = 0
		p := -1
		if b >= 0x41 && b <= 0xFE {
			p = (lead-0x81)*190 + (b - 0x41)
		}
		if cp := cjkCode(d.m.x.euckr, p); cp >= 0 {
			return mOut, []rune{cp}, nil
		}
		if isASCII(b) {
			return mError, nil, []mItem{it}
		}
		return mError, nil, nil
	}
	if isASCII(b) {
		return mOut, []rune{rune(b)}, nil
	}
	if b >= 0x81 && b <= 0xFE {
		d.lead = it
		return mContinue, nil, nil
	}
	return mError, nil, nil
}

// encode runs the standard's encode over s for name, stopping at the
// first error: the bytes, and the byte offset in s of the code point the
// error is with (-1 for none). ISO-2022-JP's errors with U+FFFD are
// errors with the code point that caused them.
func (m *cjkModel) encode(name string, s string) ([]byte, int) {
	type cpAt struct {
		cp rune
		at int
	}
	var queue []cpAt
	for i, r := range s {
		queue = append(queue, cpAt{r, i})
	}
	queue = append(queue, cpAt{-1, len(s)})
	var out []byte
	jpState := 0 // ASCII, Roman, jis0208
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		cp := it.cp
		if name == "ISO-2022-JP" {
			if cp < 0 {
				if jpState != 0 {
					jpState = 0
					out = append(out, 0x1B, 0x28, 0x42)
				}
				return out, -1
			}
			if (jpState == 0 || jpState == 1) && (cp == 0x0E || cp == 0x0F || cp == 0x1B) {
				return out, it.at
			}
			if jpState == 0 && cp <= 0x7F {
				out = append(out, byte(cp))
				continue
			}
			if jpState == 1 && ((cp <= 0x7F && cp != 0x5C && cp != 0x7E) || cp == 0xA5 || cp == 0x203E) {
				switch cp {
				case 0xA5:
					out = append(out, 0x5C)
				case 0x203E:
					out = append(out, 0x7E)
				default:
					out = append(out, byte(cp))
				}
				continue
			}
			if cp <= 0x7F && jpState != 0 {
				queue = append([]cpAt{it}, queue...)
				jpState = 0
				out = append(out, 0x1B, 0x28, 0x42)
				continue
			}
			if (cp == 0xA5 || cp == 0x203E) && jpState != 1 {
				queue = append([]cpAt{it}, queue...)
				jpState = 1
				out = append(out, 0x1B, 0x28, 0x4A)
				continue
			}
			if cp == 0x2212 {
				cp = 0xFF0D
			}
			if cp >= 0xFF61 && cp <= 0xFF9F {
				cp = rune(m.x.katakana[cp-0xFF61])
			}
			p, ok := m.jis0208[cp]
			if !ok {
				if jpState == 2 {
					queue = append([]cpAt{it}, queue...)
					jpState = 0
					out = append(out, 0x1B, 0x28, 0x42)
					continue
				}
				return out, it.at
			}
			if jpState != 2 {
				queue = append([]cpAt{it}, queue...)
				jpState = 2
				out = append(out, 0x1B, 0x24, 0x42)
				continue
			}
			out = append(out, byte(p/94+0x21), byte(p%94+0x21))
			continue
		}
		if cp < 0 {
			return out, -1
		}
		switch name {
		case "gb18030", "GBK":
			gbk := name == "GBK"
			if cp <= 0x7F {
				out = append(out, byte(cp))
				continue
			}
			if cp == 0xE5E5 {
				return out, it.at
			}
			if gbk && cp == 0x20AC {
				out = append(out, 0x80)
				continue
			}
			side := false
			for _, r := range m.x.side {
				if rune(r[0]) == cp {
					out = append(out, byte(r[1]>>8), byte(r[1]))
					side = true
				}
			}
			if side {
				continue
			}
			if p, ok := m.gb18030[cp]; ok {
				trail := p % 190
				off := 0x41
				if trail < 0x3F {
					off = 0x40
				}
				out = append(out, byte(p/190+0x81), byte(trail+off))
				continue
			}
			if gbk {
				return out, it.at
			}
			p := m.rangesPointer(cp)
			b1 := p / (10 * 126 * 10)
			p %= 10 * 126 * 10
			b2 := p / (10 * 126)
			p %= 10 * 126
			out = append(out, byte(b1+0x81), byte(b2+0x30), byte(p/10+0x81), byte(p%10+0x30))
		case "Big5":
			if cp <= 0x7F {
				out = append(out, byte(cp))
				continue
			}
			p, ok := m.big5[cp]
			if !ok {
				return out, it.at
			}
			trail := p % 157
			off := 0x62
			if trail < 0x3F {
				off = 0x40
			}
			out = append(out, byte(p/157+0x81), byte(trail+off))
		case "EUC-JP":
			switch {
			case cp <= 0x7F:
				out = append(out, byte(cp))
				continue
			case cp == 0xA5:
				out = append(out, 0x5C)
				continue
			case cp == 0x203E:
				out = append(out, 0x7E)
				continue
			case cp >= 0xFF61 && cp <= 0xFF9F:
				out = append(out, 0x8E, byte(cp-0xFF61+0xA1))
				continue
			}
			if cp == 0x2212 {
				cp = 0xFF0D
			}
			p, ok := m.jis0208[cp]
			if !ok {
				return out, it.at
			}
			out = append(out, byte(p/94+0xA1), byte(p%94+0xA1))
		case "Shift_JIS":
			switch {
			case cp <= 0x80:
				out = append(out, byte(cp))
				continue
			case cp == 0xA5:
				out = append(out, 0x5C)
				continue
			case cp == 0x203E:
				out = append(out, 0x7E)
				continue
			case cp >= 0xFF61 && cp <= 0xFF9F:
				out = append(out, byte(cp-0xFF61+0xA1))
				continue
			}
			if cp == 0x2212 {
				cp = 0xFF0D
			}
			p, ok := m.sjis[cp]
			if !ok {
				return out, it.at
			}
			lead := p / 188
			loff := 0xC1
			if lead < 0x1F {
				loff = 0x81
			}
			trail := p % 188
			off := 0x41
			if trail < 0x3F {
				off = 0x40
			}
			out = append(out, byte(lead+loff), byte(trail+off))
		case "EUC-KR":
			if cp <= 0x7F {
				out = append(out, byte(cp))
				continue
			}
			p, ok := m.euckr[cp]
			if !ok {
				return out, it.at
			}
			out = append(out, byte(p/190+0x81), byte(p%190+0x41))
		default:
			panic(name)
		}
	}
	return out, -1
}
