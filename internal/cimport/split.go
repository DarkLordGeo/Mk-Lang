// Why: Clang writes windows.h's AST as hundreds of megabytes of JSON, and
// one goroutine decoding it with encoding/json is slower than Clang writing
// it; this is the parallel half of the AST reader.
//
// What: element and batch, splitInner (the stream cutter), decodeElement,
// decodeElements and splitBatches.
//
// How: one goroutine only cuts the stream into the translation unit's
// top-level elements (a byte scanner that knows strings and nesting,
// nothing else), a few workers decode the elements, and the reader applies
// them in order. What depends on order (Clang's delta-encoded location
// state, the import markers) stays sequential and is cheap.

package cimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"

	"mk/internal/source"
)

// element is one decoded top-level node, with what the location state
// needs of its bytes.
type element struct {
	n        jnode
	err      error
	line     int    // the last "line" in the element, 0 if none
	file     string // the last "file" outside an includedFrom, "" if none
	hasFile  bool
	implicit bool
}

// batchSize is how many elements a worker decodes at once.
const batchSize = 128

type batch struct {
	raws [][]byte
	out  []element
	done chan struct{}
}

// splitInner scans the JSON of a TranslationUnitDecl from r and calls emit
// with each element of its top-level "inner" array, in order. The slices
// handed to emit are the caller's to keep.
func splitInner(r io.Reader, emit func([]byte)) error {
	buf := make([]byte, 1<<20)
	var cur []byte // the element being collected
	depth := 0
	inStr, esc := false, false
	inInner := false // inside the TU's inner array
	elemStart := -1  // the element's start in buf, -1 when it began in an earlier chunk or none
	inElem := false
	var key []byte // the last string at depth 1 (a key of the TU object)
	keyCollect := false
	for {
		nr, rerr := r.Read(buf)
		b := buf[:nr]
		if inElem {
			elemStart = 0
		}
		i := 0
		for i < len(b) {
			if inStr {
				if esc {
					// A backslash ended the previous chunk: this byte is escaped.
					esc = false
					if keyCollect {
						key = append(key, b[i])
					}
					i++
					continue
				}
				// Skip to the next quote or backslash.
				j := i
				for j < len(b) && b[j] != '"' && b[j] != '\\' {
					j++
				}
				if keyCollect {
					key = append(key, b[i:j]...)
				}
				if j == len(b) {
					i = j
					break
				}
				if b[j] == '\\' {
					if keyCollect {
						key = append(key, b[j])
					}
					if j+1 < len(b) {
						if keyCollect {
							key = append(key, b[j+1])
						}
						i = j + 2
					} else {
						esc = true
						i = j + 1
					}
					continue
				}
				inStr = false
				keyCollect = false
				i = j + 1
				continue
			}
			c := b[i]
			switch c {
			case '"':
				inStr = true
				if depth == 1 {
					key = key[:0]
					keyCollect = true
				}
			case '{', '[':
				if inInner && depth == 2 && c == '{' {
					inElem = true
					elemStart = i
				}
				if depth == 1 && c == '[' && string(key) == "inner" {
					inInner = true
				}
				depth++
			case '}', ']':
				depth--
				if depth < 0 {
					return errors.New("cimport: the AST is not balanced JSON")
				}
				if inElem && depth == 2 && c == '}' {
					cur = append(cur, b[elemStart:i+1]...)
					emit(cur)
					cur = nil
					inElem = false
					elemStart = -1
				}
				if inInner && depth == 1 {
					inInner = false
				}
			}
			i++
		}
		if inElem && elemStart >= 0 {
			cur = append(cur, b[elemStart:]...)
		}
		if rerr == io.EOF {
			if depth != 0 || inStr {
				return errors.New("cimport: the AST ended early")
			}
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// decodeElement decodes one element and its location facts.
func decodeElement(raw []byte, e *element) {
	if err := json.Unmarshal(raw, &e.n); err != nil {
		e.err = err
		return
	}
	e.line, e.file, e.hasFile = lastLoc(raw)
	if e.n.IsImplicit {
		e.implicit = true
		return
	}
	prewarm(&e.n, 0)
}

// prewarm decodes a node's children ahead of the sequential reader, a few
// levels down (records nest), never into a function body.
func prewarm(n *jnode, depth int) {
	if depth > 3 || n.Kind == "CompoundStmt" {
		return
	}
	for i := range n.children() {
		prewarm(&n.kids[i], depth+1)
	}
}

// lastLoc is the last "line" value in raw (0 if none) and the last "file"
// outside an includedFrom object.
func lastLoc(raw []byte) (line int, file string, hasFile bool) {
	if i := lastKey(raw, `"line":`, false); i >= 0 {
		j := i
		for j < len(raw) && isSpaceB(raw[j]) {
			j++
		}
		k := j
		for k < len(raw) && raw[k] >= '0' && raw[k] <= '9' {
			k++
		}
		if v, err := strconv.Atoi(string(raw[j:k])); err == nil {
			line = v
		}
	}
	if i := lastKey(raw, `"file":`, true); i >= 0 {
		dec := json.NewDecoder(bytes.NewReader(raw[i:]))
		if dec.Decode(&file) == nil {
			hasFile = true
		}
	}
	return line, file, hasFile
}

// decodeElements splits r's top-level elements and decodes them on
// workers, calling apply with each in order. It returns the first error of
// the split, a decode or apply.
func decodeElements(r io.Reader, apply func(*element) error) error {
	workers := runtime.GOMAXPROCS(0) / 2
	if workers < 2 {
		workers = 2
	}
	if workers > 6 {
		workers = 6
	}
	work := make(chan *batch, workers*2)
	order := make(chan *batch, workers*4)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			source.Guard("C import AST decode", source.Span{}, func() {
				for bt := range work {
					decodeBatch(bt)
				}
			})
		}()
	}
	splitErr := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		defer close(order)
		defer close(work)
		if ie := source.Guard("C import AST split", source.Span{}, func() { splitBatches(r, order, work, stop, splitErr) }); ie != nil {
			splitErr <- errors.New("cimport: internal error splitting the AST: " + ie.String())
		}
	}()
	var aerr error
	for bt := range order {
		if aerr != nil {
			continue // after a stop, a batch may never reach a worker
		}
		<-bt.done
		for i := range bt.out {
			e := &bt.out[i]
			if e.err != nil {
				aerr = e.err
				break
			}
			if err := apply(e); err != nil {
				aerr = err
				break
			}
		}
		if aerr != nil {
			close(stop)
		}
	}
	wg.Wait()
	serr := <-splitErr
	if aerr != nil {
		return aerr
	}
	if serr != nil {
		return fmt.Errorf("cimport: reading the AST: %w", serr)
	}
	return nil
}

// decodeBatch decodes a batch's elements and closes its done; a panic in
// the decoder is the batch's error, never a crash or a hang.
func decodeBatch(bt *batch) {
	defer close(bt.done)
	bt.out = make([]element, len(bt.raws))
	if ie := source.Guard("C import AST element", source.Span{}, func() {
		for i, raw := range bt.raws {
			decodeElement(raw, &bt.out[i])
		}
	}); ie != nil {
		bt.out = []element{{err: errors.New("cimport: internal error decoding the AST: " + ie.String())}}
	}
	bt.raws = nil
}

// splitBatches cuts r's elements into batches, each sent to order and then
// to work, until r ends or stop closes; it sends the split's error.
func splitBatches(r io.Reader, order, work chan<- *batch, stop <-chan struct{}, splitErr chan<- error) {
	cur := &batch{done: make(chan struct{})}
	send := func() bool {
		select {
		case order <- cur:
		case <-stop:
			return false
		}
		select {
		case work <- cur:
		case <-stop:
			return false
		}
		cur = &batch{done: make(chan struct{})}
		return true
	}
	stopped := false
	err := splitInner(r, func(raw []byte) {
		if stopped {
			return
		}
		cur.raws = append(cur.raws, raw)
		if len(cur.raws) == batchSize && !send() {
			stopped = true
		}
	})
	if err == nil && !stopped && len(cur.raws) > 0 {
		send()
	}
	splitErr <- err
}
