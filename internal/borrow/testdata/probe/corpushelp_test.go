// The corpus helpers of internal/check's tests (fuzz_test.go, corpus_test.go),
// copied: the doc blocks, the test prelude and the corpus prelude.

package probe

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/syntax"
)

// stdStubs are std modules a judged block imports, declared as far as the
// block needs (the real std is not loaded here).
var stdStubs = map[string]string{
	"std.fs": "export error Error\nexport Remove(io: Io, path: str): Error!void\n" +
		"export ReadText(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a\n" +
		// Reference §18's File, linear (§3), as far as the three Save
		// blocks (04-reference, 04d-cookbook, 05-memory-model) use it.
		"export linear struct File {\n\th: int\n}\n" +
		"export Create(io: Io, p: str): Error!File\n" +
		"export File.WriteAll(mut self, io: Io, data: []u8): Error!void\n" +
		"export File.Sync(mut self, io: Io): Error!void\n" +
		"export File.Close(self, io: Io): Error!void\n" +
		"export File.Abandon(self)\n",
}

// docBlocks reads every fenced mk block under doc/, keyed by
// "file:line" of its opening fence, in file and line order.
func docBlocks(t testing.TB) (keys []string, texts map[string]string) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "doc")
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no doc files: %v", err)
	}
	sort.Strings(files)
	texts = map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "```mk" {
				continue
			}
			start := i + 1
			j := start
			for j < len(lines) && strings.TrimSpace(lines[j]) != "```" {
				j++
			}
			rel, _ := filepath.Rel(root, f)
			key := filepath.ToSlash(rel) + ":" + itoa(i+1)
			keys = append(keys, key)
			texts[key] = strings.Join(lines[start:j], "\n") + "\n"
			i = j
		}
	}
	return keys, texts
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// testPrelude is the test prelude (std/corpus/testprelude.mk) that every doc
// block and every conformance unit with `// prelude: yes` compiles with.
func testPrelude(t testing.TB) string {
	keys, texts := docBlocks(t)
	for _, k := range keys {
		if strings.HasPrefix(k, "reference.md:") && strings.Contains(texts[k], "MakePoint(): Point\n") {
			return texts[k]
		}
	}
	t.Fatal("no test prelude block in doc/reference.md")
	return ""
}

// corpusPrelude is fakePrelude() with reference §18's std.core surface
// added: System's fields, and every function line before the block's
// "// std.fs" comment, exported and bodiless (std's intrinsic form). The
// names the fake prelude declares itself (Print, Arena.Fmt, Writer.Str,
// Cell's functions, the interfaces and Cell) keep the fake's text.
func corpusPrelude(t testing.TB) string {
	keys, texts := docBlocks(t)
	var lib string
	for _, k := range keys {
		if strings.HasPrefix(k, "reference.md:") && strings.Contains(texts[k], "System.Stdout(&self): Writer\n") {
			lib = texts[k]
		}
	}
	if lib == "" {
		t.Fatal("no library surface block in doc/reference.md")
	}
	core := fakePrelude()
	const fakeSystem = "export struct System {}\n"
	if !strings.Contains(core, fakeSystem) {
		t.Fatal("fakePrelude declares System differently")
	}
	var b strings.Builder
	depth := 0
	inSystem := false
	for _, l := range strings.Split(lib, "\n") {
		if strings.HasPrefix(l, "// std.fs") {
			break
		}
		switch {
		case strings.HasPrefix(l, "struct System {"):
			inSystem = true
			b.WriteString("export " + l + "\n")
			continue
		case inSystem:
			b.WriteString(l + "\n")
			if l == "}" {
				inSystem = false
			}
			continue
		}
		if depth > 0 || strings.HasSuffix(l, "{") {
			depth += strings.Count(l, "{") - strings.Count(l, "}")
			continue
		}
		if l == "" || strings.HasPrefix(l, "//") || strings.HasPrefix(l, " ") || !strings.Contains(l, "(") {
			continue
		}
		if strings.HasPrefix(l, "Print[") || strings.HasPrefix(l, "Arena.Fmt") || strings.HasPrefix(l, "Writer.Str(") || strings.HasPrefix(l, "Cell[") {
			continue
		}
		b.WriteString("export " + l + "\n")
	}
	core = strings.Replace(core, fakeSystem, "", 1)
	// The iterators' fields as std lays them out, so the
	// surface's `from self.text` and `from self.map` clauses resolve.
	for _, r := range [][2]string{
		{"export struct Lines {}\n", "export view struct Lines {\n\ttext: str\n\tpos: int\n}\n"},
		{"export struct Split {}\n", "export view struct Split {\n\ttext: str\n\tpos: int\n}\n"},
		{"export struct Words {}\n", "export view struct Words {\n\ttext: str\n\tpos: int\n}\n"},
		{"export struct Scalars {}\n", "export view struct Scalars {\n\ttext: str\n\tpos: int\n}\n"},
		{"export view struct MapIter[K, V] {\n\tk: *K\n\tv: *V\n}\n", "export view struct MapIter[K, V] {\n\tmap: &Map[K, V]\n\ti: int\n}\n"},
		{"export view struct TextMapIter[V] {\n\tp: *V\n}\n", "export view struct TextMapIter[V] {\n\tmap: &TextMap[V]\n\ti: int\n}\n"},
		// Reference §18's enums, and Format for every integer width std implements it for.
		{"export enum Arch { A }\n", "export enum Arch { X64\n\tArm64\n\tRiscv64 }\n"},
		{"export enum Os { A }\n", "export enum Os { Linux\n\tWindows\n\tMacos\n\tFreestanding }\n" +
			"impl Format for i32 {\n\tFormat(&self, w: mut Writer): WriteError!void {\n\t}\n}\n"},
	} {
		if !strings.Contains(core, r[0]) {
			t.Fatalf("fakePrelude has no %q", r[0])
		}
		core = strings.Replace(core, r[0], r[1], 1)
	}
	core += b.String()
	sm := source.NewSourceMap()
	if _, ds := syntax.ParseFile(sm.Add("core.mk", []byte(core)), source.NewInterner(), syntax.Options{AllowBodiless: true}); len(ds) > 0 {
		t.Fatalf("the corpus prelude does not parse: %s at %d", ds[0].Message, ds[0].At.Start)
	}
	return core
}

// stubPaths are stdStubs' keys in byte order, so modules register in a
// fixed order.
func stubPaths() []string {
	var out []string
	for p := range stdStubs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
