// Why: private package hosts need credentials, and users already keep them
// in a netrc file for git and curl.
//
// What: Netrc with Lookup, ParseNetrc, NetrcPath and LoadNetrc.
//
// How: a netrc file gives a login and password
// for a host. The file is NETRC, else ~/.netrc, else (on Windows)
// %USERPROFILE%\_netrc. Only "machine" entries are used: a "default" entry
// would send one password to every host, so it is ignored, and "macdef"
// bodies are skipped. Quoted values are read as curl reads them. A
// credential appears in no error this package makes.

package git

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Netrc maps a host name (lowercase, no port) to its login and password.
type Netrc struct {
	hosts map[string][2]string
}

// Lookup returns the login and password for host (a host name, with or
// without a port; the port is ignored, as netrc names machines).
func (n *Netrc) Lookup(host string) (login, password string, ok bool) {
	if n == nil {
		return "", "", false
	}
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	c, ok := n.hosts[strings.ToLower(host)]
	return c[0], c[1], ok
}

// splitHostPort splits "host:port", leaving a bare host or "[v6]" alone.
func splitHostPort(s string) (string, string, error) {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i], strings.TrimPrefix(s[i+1:], ":"), nil
		}
		return s, "", nil
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && strings.Count(s, ":") == 1 {
		return s[:i], s[i+1:], nil
	}
	return s, "", nil
}

// ParseNetrc reads netrc text. The first entry for a machine wins, as in
// curl and git.
func ParseNetrc(data []byte) *Netrc {
	n := &Netrc{hosts: map[string][2]string{}}
	var toks []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	inMacro := false
	for sc.Scan() {
		line := sc.Text()
		if inMacro {
			if strings.TrimSpace(line) == "" {
				inMacro = false
			}
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			continue
		}
		f := netrcFields(line)
		for i := 0; i < len(f); i++ {
			if f[i] == "macdef" {
				inMacro = true
				toks = append(toks, f[i])
				break
			}
			toks = append(toks, f[i])
		}
	}
	host, login, pass := "", "", ""
	inDefault := false
	flush := func() {
		if host != "" && !inDefault {
			k := strings.ToLower(host)
			if _, dup := n.hosts[k]; !dup {
				n.hosts[k] = [2]string{login, pass}
			}
		}
		host, login, pass, inDefault = "", "", "", false
	}
	for i := 0; i < len(toks); i++ {
		val := func() string {
			if i+1 < len(toks) {
				i++
				return toks[i]
			}
			return ""
		}
		switch toks[i] {
		case "machine":
			flush()
			host = val()
		case "default":
			flush()
			inDefault = true
		case "login":
			login = val()
		case "password":
			pass = val()
		case "account":
			val()
		case "macdef": // its name and body were not tokenized
			flush()
		}
	}
	flush()
	return n
}

// NetrcPath is the netrc file a client reads: NETRC when set, else
// <home>/.netrc when it exists, else on Windows <USERPROFILE>/_netrc.
// getenv reads the environment (os.Getenv outside tests).
func NetrcPath(getenv func(string) string, windows bool) string {
	if p := getenv("NETRC"); p != "" {
		return p
	}
	home := getenv("HOME")
	if windows && home == "" {
		home = getenv("USERPROFILE")
	}
	if home != "" {
		p := filepath.Join(home, ".netrc")
		if _, err := os.Stat(p); err == nil || !windows {
			return p
		}
	}
	if windows {
		if up := getenv("USERPROFILE"); up != "" {
			return filepath.Join(up, "_netrc")
		}
	}
	return ""
}

// LoadNetrc reads the netrc file at path; a missing file (or path "")
// gives no credentials and no error.
func LoadNetrc(path string) (*Netrc, error) {
	if path == "" {
		return ParseNetrc(nil), nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ParseNetrc(nil), nil
	}
	if err != nil {
		return nil, errorf(ReasonInvalid, "the netrc file %s cannot be read: %v", filepath.Base(path), errors.Unwrap(err))
	}
	return ParseNetrc(b), nil
}

// netrcFields splits a netrc line into tokens as curl (7.84 and later)
// does: whitespace separates them, and a token that starts with a double
// quote runs to the closing quote, with \", \\, \n, \r and \t escapes.
func netrcFields(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		c := line[i]
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		var tok strings.Builder
		if c != '"' {
			for i < len(line) && line[i] != ' ' && line[i] != '\t' && line[i] != '\r' {
				tok.WriteByte(line[i])
				i++
			}
			out = append(out, tok.String())
			continue
		}
		for i++; i < len(line) && line[i] != '"'; i++ {
			if line[i] == '\\' && i+1 < len(line) {
				i++
				switch line[i] {
				case 'n':
					tok.WriteByte('\n')
				case 'r':
					tok.WriteByte('\r')
				case 't':
					tok.WriteByte('\t')
				default:
					tok.WriteByte(line[i])
				}
				continue
			}
			tok.WriteByte(line[i])
		}
		i++ // the closing quote
		out = append(out, tok.String())
	}
	return out
}
