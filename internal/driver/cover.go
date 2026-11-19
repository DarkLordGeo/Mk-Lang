// Why: mk test --cover must turn the counters codegen plants into line
// coverage per file, and only the driver knows the executable, the
// children it runs and the package's modules.
//
// What: readCoverImage (coverImage, coverRow), coverEnv and coverAsk (the
// children's MK_COVER), addCounts, coverReport, and CoverReport with its
// WriteText and WriteLcov.
//
// How: mk test --cover builds with codegen's counters (Config.Cover,
// internal/codegen/cover.go), finds their section in the runner
// executable, gives each child MK_COVER (std/rt/cover.mk: the section's
// link-time address and size, the image's link-time base, and a counts
// file), merges the counts the children write, maps each counter to its
// line through the descriptor rows, and reports line coverage per file,
// as text and as lcov (--cover-out).
//
// A line's count is the largest count of the blocks that start on it, and
// a line is covered when that count is above 0: a line holding the start
// of several blocks (`a && b`, a loop head) is hit when any of them ran.
// A block's line is the one codegen records (its first statement's, else
// its terminator's), so a block that begins with code of an enclosing
// construct counts on that construct's line; and a function the build
// never emits (nothing calls it) has no row, so it is in no total. Both
// are codegen's (cover.go there) to refine.

package driver

import (
	"debug/elf"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The counter and row sections in a linked image: lld-link merges the
// objects' .mkcc$M and .mkcv$M into .mkcc and .mkcv; ELF keeps mk_cnts and
// mk_cover.
const (
	coverCountersCOFF = ".mkcc"
	coverRowsCOFF     = ".mkcv"
	coverCountersELF  = "mk_cnts"
	coverRowsELF      = "mk_cover"
	coverRowSize      = 56
	// CoverEnv is the variable std.rt's reader reads.
	CoverEnv = "MK_COVER"
)

// coverImage is what the driver reads from a runner built with counters.
type coverImage struct {
	counters, size uint64 // the counter section's address and size at the link-time base
	base           uint64 // the link-time base: ImageBase, or the lowest PT_LOAD address
	rows           []coverRow
}

// coverRow is one function's descriptor row: its first counter's index,
// its key, its file and each block's 1-based line (0: none).
type coverRow struct {
	first int
	key   string
	file  string
	lines []int32
}

// imageReader reads bytes at a link-time virtual address of an image.
type imageReader func(va uint64, n int) ([]byte, error)

// readCoverImage reads the counter section and the rows of exe; a nil
// image (and no error) when it has no counters.
func readCoverImage(exe string) (*coverImage, error) {
	if f, err := pe.Open(exe); err == nil {
		defer f.Close()
		oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
		if !ok {
			return nil, errors.New("cover: not a PE32+ image")
		}
		cnt, rows := f.Section(coverCountersCOFF), f.Section(coverRowsCOFF)
		if cnt == nil || rows == nil {
			return nil, nil
		}
		read := func(va uint64, n int) ([]byte, error) {
			for _, s := range f.Sections {
				lo := oh.ImageBase + uint64(s.VirtualAddress)
				if va >= lo && va+uint64(n) <= lo+uint64(max(s.VirtualSize, s.Size)) {
					b := make([]byte, n)
					off := int64(va - lo)
					if off+int64(n) > int64(s.Size) { // in the zero-filled tail
						if off < int64(s.Size) {
							_, err := s.ReadAt(b[:int64(s.Size)-off], off)
							return b, err
						}
						return b, nil
					}
					_, err := s.ReadAt(b, off)
					return b, err
				}
			}
			return nil, fmt.Errorf("cover: address 0x%x is in no section", va)
		}
		img := &coverImage{counters: oh.ImageBase + uint64(cnt.VirtualAddress), size: uint64(cnt.VirtualSize), base: oh.ImageBase}
		data, err := rows.Data()
		if err != nil {
			return nil, err
		}
		return img, img.readRows(data[:min(len(data), int(rows.VirtualSize))], read)
	}
	f, err := elf.Open(exe)
	if err != nil {
		return nil, fmt.Errorf("cover: %s is neither PE nor ELF: %v", exe, err)
	}
	defer f.Close()
	cnt, rows := f.Section(coverCountersELF), f.Section(coverRowsELF)
	if cnt == nil || rows == nil {
		return nil, nil
	}
	img := &coverImage{counters: cnt.Addr, size: cnt.Size}
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && (img.base == 0 || p.Vaddr < img.base) {
			img.base = p.Vaddr
		}
	}
	read := func(va uint64, n int) ([]byte, error) {
		for _, s := range f.Sections {
			if s.Addr != 0 && va >= s.Addr && va+uint64(n) <= s.Addr+s.Size {
				b := make([]byte, n)
				if s.Type == elf.SHT_NOBITS {
					return b, nil
				}
				_, err := s.ReadAt(b, int64(va-s.Addr))
				return b, err
			}
		}
		return nil, fmt.Errorf("cover: address 0x%x is in no section", va)
	}
	data, err := rows.Data()
	if err != nil {
		return nil, err
	}
	return img, img.readRows(data, read)
}

// readRows decodes the descriptor rows (internal/codegen/cover.go's
// coverRowType: counters, blocks, key, key length, file, file length,
// lines), skipping the zero padding a linker may leave between them.
func (img *coverImage) readRows(data []byte, read imageReader) error {
	le := binary.LittleEndian
	for off := 0; off+coverRowSize <= len(data); off += 8 {
		cp := le.Uint64(data[off:])
		if cp == 0 {
			continue
		}
		nb := int(le.Uint64(data[off+8:]))
		kp, kn := le.Uint64(data[off+16:]), int(le.Uint64(data[off+24:]))
		fp, fn := le.Uint64(data[off+32:]), int(le.Uint64(data[off+40:]))
		lp := le.Uint64(data[off+48:])
		if cp < img.counters || cp >= img.counters+img.size || nb <= 0 || nb > 1<<24 || kn > 1<<16 || fn > 1<<16 {
			return fmt.Errorf("cover: a malformed row at offset %d", off)
		}
		key, err := read(kp, kn)
		if err != nil {
			return err
		}
		file, err := read(fp, fn)
		if err != nil {
			return err
		}
		lb, err := read(lp, 4*nb)
		if err != nil {
			return err
		}
		row := coverRow{first: int((cp - img.counters) / 8), key: string(key), file: string(file), lines: make([]int32, nb)}
		for i := range row.lines {
			row.lines[i] = int32(le.Uint32(lb[4*i:]))
		}
		img.rows = append(img.rows, row)
		off += coverRowSize - 8
	}
	return nil
}

// coverEnv is a child's MK_COVER: the section, the base and the counts
// file ("-" for none), the path as the child sees it.
func (img *coverImage) coverEnv(file string) string {
	return CoverEnv + "=0x" + strconv.FormatUint(img.counters, 16) + " " + strconv.FormatUint(img.size, 10) +
		" 0x" + strconv.FormatUint(img.base, 16) + " " + file
}

// coverAsk asks a child that is about to be stopped for its counts: it
// makes <file>.req and waits up to 2 s for std.rt's writer thread to
// answer with <file>.ack after writing them.
//
// It reports whether the answer came (false: the child had no writer
// thread, or did not answer in time, and its counts are its last periodic
// write).
func coverAsk(file string) bool {
	if err := os.WriteFile(file+".req", nil, 0o644); err != nil {
		return false
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(file + ".ack"); err == nil {
			return true
		}
	}
	return false
}

// addCounts adds the counts file at path to sum (one u64 per counter);
// a missing file adds nothing (the child ran no Main, or ended before its
// first write: a panic within 100 ms). A file shorter than the counters
// adds what it holds and is reported, so a lost count is never silent.
func addCounts(sum []uint64, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no counts: the test ended before its counts were first written")
		}
		return err
	}
	for i := 0; i < len(sum) && 8*i+8 <= len(b); i++ {
		sum[i] += binary.LittleEndian.Uint64(b[8*i:])
	}
	if len(b) < 8*len(sum) {
		return fmt.Errorf("a short counts file: %d bytes of %d", len(b), 8*len(sum))
	}
	return nil
}

// CoverFile is one source file's line coverage.
type CoverFile struct {
	File    string        `json:"file"`
	Lines   int           `json:"lines"`   // lines that start a block
	Covered int           `json:"covered"` // of them, those with a count above 0
	Counts  map[int]int64 `json:"-"`       // line to its count
}

// CoverReport is mk test --cover's result, files in path order.
type CoverReport struct {
	Files []CoverFile `json:"files"`
}

// coverReport maps the summed counts to lines.
//
// Only rows of mods, the modules of the package under test, count (as Go
// reports its package alone): a dependency's rows, the generated
// runner's and the test blocks' (their clones are the runner module's,
// testrunner.go) are left out, so every file is the package's own and
// test-block lines are in no total.
func (img *coverImage) coverReport(sum []uint64, mods []string) *CoverReport {
	byFile := map[string]*CoverFile{}
	for _, r := range img.rows {
		if !rowInModules(r.key, mods) {
			continue
		}
		cf := byFile[r.file]
		if cf == nil {
			cf = &CoverFile{File: r.file, Counts: map[int]int64{}}
			byFile[r.file] = cf
		}
		for i, l := range r.lines {
			if l <= 0 || r.first+i >= len(sum) {
				continue
			}
			c := int64(sum[r.first+i])
			if old, ok := cf.Counts[int(l)]; !ok || c > old {
				cf.Counts[int(l)] = c
			}
		}
	}
	rep := &CoverReport{}
	for _, cf := range byFile {
		cf.Lines = len(cf.Counts)
		for _, c := range cf.Counts {
			if c > 0 {
				cf.Covered++
			}
		}
		rep.Files = append(rep.Files, *cf)
	}
	slices.SortFunc(rep.Files, func(a, b CoverFile) int { return strings.Compare(a.File, b.File) })
	return rep
}

// rowInModules reports whether a row's key, module path then the item's
// name, belongs to one of mods.
func rowInModules(key string, mods []string) bool {
	for _, m := range mods {
		if strings.HasPrefix(key, m+".") {
			return true
		}
	}
	return false
}

// WriteText prints one line per file, "file: covered/lines lines (p%)",
// and a total.
func (r *CoverReport) WriteText(w io.Writer) error {
	var b strings.Builder
	lines, covered := 0, 0
	for _, f := range r.Files {
		fmt.Fprintf(&b, "coverage: %s: %d/%d lines (%s)\n", f.File, f.Covered, f.Lines, percent(f.Covered, f.Lines))
		lines += f.Lines
		covered += f.Covered
	}
	fmt.Fprintf(&b, "coverage: total: %d/%d lines (%s)\n", covered, lines, percent(covered, lines))
	_, err := io.WriteString(w, b.String())
	return err
}

func percent(a, n int) string {
	if n == 0 {
		return "no lines"
	}
	return strconv.FormatFloat(100*float64(a)/float64(n), 'f', 1, 64) + "%"
}

// WriteLcov writes r as lcov's tracefile: per file SF (root joined with
// the file's package path), DA per line in order, LF and LH.
func (r *CoverReport) WriteLcov(w io.Writer, root string) error {
	var b strings.Builder
	b.WriteString("TN:\n")
	for _, f := range r.Files {
		fmt.Fprintf(&b, "SF:%s\n", filepath.ToSlash(filepath.Join(root, filepath.FromSlash(f.File))))
		ls := make([]int, 0, len(f.Counts))
		for l := range f.Counts {
			ls = append(ls, l)
		}
		slices.Sort(ls)
		for _, l := range ls {
			fmt.Fprintf(&b, "DA:%d,%d\n", l, f.Counts[l])
		}
		fmt.Fprintf(&b, "LF:%d\nLH:%d\nend_of_record\n", f.Lines, f.Covered)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
