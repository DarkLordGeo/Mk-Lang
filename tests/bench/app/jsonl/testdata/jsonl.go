// jsonl, Go, std: the task of ../README.md with encoding/json's v1 API (in
// Go 1.27 it runs on encoding/json/v2's engine with the v1 options; the
// README's Cells say why the row keeps v1). The file is
// read through a bufio.Scanner over a 64 KiB buffer, one line at a time;
// json.Unmarshal decodes each line into a fresh Event (struct tags name the
// members). Kept events are grouped by user in a map[string]*Group, the
// groups are sorted with slices.SortFunc, and the Report is written by
// json.NewEncoder (compact JSON, members in field order, then a line feed;
// SetEscapeHTML(false), though the report holds no <, > or &).
// Usage: jsonl <file> [<lines>].
package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
)

type Event struct {
	ID     int64    `json:"id"`
	User   string   `json:"user"`
	Event  string   `json:"event"`
	Status int64    `json:"status"`
	Bytes  int64    `json:"bytes"`
	Ms     int64    `json:"ms"`
	Delta  int64    `json:"delta"`
	Ok     bool     `json:"ok"`
	Tags   []string `json:"tags"`
	Msg    string   `json:"msg"`
}

type Group struct {
	User  string `json:"user"`
	Count int64  `json:"count"`
	First int64  `json:"first"`
	Bytes int64  `json:"bytes"`
	MsMin int64  `json:"ms_min"`
	MsMax int64  `json:"ms_max"`
	Delta int64  `json:"delta"`
	Tags  int64  `json:"tags"`
	Msg   int64  `json:"msg"`
	Buys  int64  `json:"buys"`
	Fails int64  `json:"fails"`
}

type Report struct {
	Lines   int64   `json:"lines"`
	Kept    int64   `json:"kept"`
	Dropped int64   `json:"dropped"`
	Groups  []Group `json:"groups"`
}

func (g *Group) add(e *Event) {
	g.Count++
	g.Bytes += e.Bytes
	g.MsMin = min(g.MsMin, e.Ms)
	g.MsMax = max(g.MsMax, e.Ms)
	g.Delta += e.Delta
	g.Tags += int64(len(e.Tags))
	g.Msg += int64(len(e.Msg))
	if e.Event == "buy" {
		g.Buys++
	}
	if e.Status >= 400 {
		g.Fails++
	}
}

func run() error {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		return fmt.Errorf("usage: jsonl <file> [<lines>]")
	}
	limit := int64(-1)
	if len(os.Args) == 3 {
		n, err := strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil {
			return err
		}
		limit = n
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	groups := map[string]*Group{}
	var n, kept int64
	for n != limit && sc.Scan() {
		n++
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		if !e.Ok {
			continue
		}
		kept++
		g := groups[e.User]
		if g == nil {
			g = &Group{User: e.User, First: e.ID, MsMin: e.Ms, MsMax: e.Ms}
			groups[e.User] = g
		}
		g.add(&e)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	gs := make([]Group, 0, len(groups))
	for _, g := range groups {
		gs = append(gs, *g)
	}
	slices.SortFunc(gs, func(a, b Group) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return cmp.Compare(a.User, b.User)
	})
	w := bufio.NewWriterSize(os.Stdout, 64<<10)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Report{Lines: n, Kept: kept, Dropped: n - kept, Groups: gs}); err != nil {
		return err
	}
	return w.Flush()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "jsonl:", err)
		os.Exit(1)
	}
}
