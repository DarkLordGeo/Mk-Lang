// Why: a fresh toolchain must install its first-party web framework from the
// seed alone, with no network.
// What: TestFrameworksRow: `mk install web` in a new package with the network
// off, and the server built from it answering a request.
// How: lays out a toolchain as a release does (bin/mk and seed/), points mk's
// proxies at a closed port, and keeps every cache under the test's
// temporary directory.

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestFrameworksRow checks the first-party frameworks offline: with the
// network disabled, `mk install web` in a new package adds the seeded version, and
// the server built from it answers a request.
//
// The toolchain is laid out as a release lays it out, <tc>/bin/mk and
// <tc>/seed/, with an mk built from this tree and a seed built by `seed` from
// kits/web. The tag is served, so the test runs offline, by a synthetic git
// server under the reserved address example.com/mk-test/web; the server is
// closed before mk runs, and mk's proxies point at a closed port, so any
// fetch would fail. Every cache and home directory of the mk processes is
// under the test's temporary directory. The server listens on a loopback port
// the test picks.
func TestFrameworksRow(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}

	// The toolchain: bin/mk.
	tc := filepath.Join(root, "tc")
	mk := filepath.Join(tc, "bin", "mk"+exe)
	build := exec.Command(goTool(t), "build", "-o", mk, "./cmd/mk")
	build.Dir = repo
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/mk: %v\n%s", err, b)
	}

	// The seed: kits/web at the tag v0.1.0 of the synthetic repository.
	const version = "0.1.0"
	kits := filepath.Join(repo, "kits")
	tree, err := kitTree(filepath.Join(kits, "web"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range tree.Files {
		files[f.Path] = string(f.Data)
	}
	gs := newGitServer(t)
	gs.tag("mk-test/web", version, files)
	src := gs.source("mk-test/web")
	list := writeList(t, "# the release's first-party list\nweb "+src+" v"+version+"\n")
	seed := filepath.Join(tc, "seed")
	t.Cleanup(func() { makeWritable(seed) })
	es, err := buildSeed(seedJob{Kits: kits, List: list, Out: seed, Fetcher: seedFetcher(gs)})
	if err != nil {
		t.Fatal(err)
	}
	gs.srv.Close()
	seedLine := es[0].line()

	// mk's environment: no network, nothing of the user's.
	home := filepath.Join(root, "home")
	netrc := filepath.Join(home, "netrc")
	os.MkdirAll(home, 0o755)
	os.WriteFile(netrc, nil, 0o600)
	env := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "MK_CACHE", "MK_OFFLINE", "MK_STD", "LOCALAPPDATA", "HOME", "USERPROFILE", "XDG_CACHE_HOME", "NETRC",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"MK_CACHE="+filepath.Join(root, "cache"),
		"LOCALAPPDATA="+filepath.Join(home, "lad"),
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, "xdg"),
		"NETRC="+netrc,
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"ALL_PROXY=http://127.0.0.1:1",
	)
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(mk, args...)
		cmd.Dir = dir
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("mk %s: %v\n%s", strings.Join(args, " "), err, b)
		}
		return string(b)
	}

	// A new package; mk install web from the seed.
	run(root, "new", "app")
	app := filepath.Join(root, "app")
	out := run(app, "install", "web")
	if !strings.Contains(out, "added web ("+src+" v"+version+")") || strings.Contains(out, "needs a grant") {
		t.Errorf("mk install web printed (want the line added and no grant):\n%s", out)
	}
	man, _ := os.ReadFile(filepath.Join(app, "pkg.mk"))
	if !strings.Contains(string(man), `pkg.Dep(name: "web", source: "`+src+`", version: "`+version+`")`) {
		t.Errorf("pkg.mk after install:\n%s", man)
	}
	sum, _ := os.ReadFile(filepath.Join(app, "pkg.sum"))
	if want := fmt.Sprintf("%s v%s %s %s", src, version, es[0].Commit, es[0].Hash); !strings.Contains(string(sum), want) {
		t.Errorf("pkg.sum %q, want the seed's line %q (seed: %s)", sum, want, seedLine)
	}

	// The server: the example's main module, built against the installed
	// web, on a port the test picks.
	main, err := os.ReadFile(filepath.Join(kits, "web", "examples", "hello", "src", "main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "src", "main.mk"), main, 0o644); err != nil {
		t.Fatal(err)
	}
	run(app, "build", ".")
	bin := filepath.Join(app, "app"+exe)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	srv := exec.Command(bin, addr)
	srv.Dir = app
	srv.Env = env
	stdout, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	srv.Stderr = &stderr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Process.Kill()
		srv.Wait()
	})
	first, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(first) != "serving on "+addr {
		t.Fatalf("the server printed %q, stderr %q", first, stderr.String())
	}
	go io.Copy(io.Discard, stdout)

	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	get := func(method, path, body string) (int, http.Header, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			req, _ := http.NewRequestWithContext(ctx, method, "http://"+addr+path, strings.NewReader(body))
			res, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					t.Fatalf("%s %s: %v (stderr %q)", method, path, err, stderr.String())
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return res.StatusCode, res.Header, string(b)
		}
	}
	for _, c := range []struct {
		method, path, body string
		code               int
		want               string
	}{
		{"GET", "/", "", 200, "hello from web"},
		{"GET", "/greet?name=gate+row", "", 200, `{"text":"hello, gate row"}`},
		{"GET", "/items/7", "", 200, `{"id":7,"name":"item 7","qty":1}`},
		{"POST", "/items", `{"name":"bolt","qty":12}`, 201, `{"id":1,"name":"bolt","qty":12}`},
		{"POST", "/items", `{"name":"","qty":12}`, 422, ""},
		{"POST", "/items", `not json`, 400, ""},
		{"GET", "/nothing", "", 404, ""},
	} {
		code, h, body := get(c.method, c.path, c.body)
		if code != c.code || body != c.want {
			t.Errorf("%s %s: %d %q, want %d %q", c.method, c.path, code, body, c.code, c.want)
		}
		if code < 400 && h.Get("X-Served-By") != "mk-web" {
			t.Errorf("%s %s: X-Served-By %q, want the middleware's mk-web", c.method, c.path, h.Get("X-Served-By"))
		}
	}

	// The kit's own test blocks and its example's (the generated Body and
	// Validate), on a copy of the kit's tree, so no gen/ view or binary
	// lands in the repository.
	copyKit := filepath.Join(root, "kitcopy", "web")
	if err := tree.Write(copyKit); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{copyKit, filepath.Join(copyKit, "examples", "hello")} {
		if out := run(dir, "test", "."); !strings.Contains(out, " passed, 0 failed") {
			t.Errorf("mk test %s:\n%s", dir, out)
		}
	}

	// A rule naming no field is a compile-time error in the fetched
	// generator's output (E0903 at the gen line).
	bad := "import web\n\nstruct Bad {\n    export n: i64\n}\n\ngen web.Validate(Bad, \"nope: nonempty\")\n"
	if err := os.WriteFile(filepath.Join(app, "src", "bad.mk"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(mk, "check", ".")
	cmd.Dir = app
	cmd.Env = env
	b, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(b), "E0903") || !strings.Contains(string(b), "names a field the struct does not have") {
		t.Errorf("mk check of a bad rule: %v\n%s", err, b)
	}

	// A bound outside an integer field's range is the generator's E0903
	// too, not E0905 in the generated comparison.
	bad = "import web\n\nstruct Bad {\n    export n: u8\n}\n\ngen web.Validate(Bad, \"n: min 0, max 1000\")\n"
	if err := os.WriteFile(filepath.Join(app, "src", "bad.mk"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(mk, "check", ".")
	cmd.Dir = app
	cmd.Env = env
	b, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(b), "E0903") || strings.Contains(string(b), "E0905") || !strings.Contains(string(b), "the field type's range") {
		t.Errorf("mk check of an out-of-range bound: %v\n%s", err, b)
	}
}
