// tls-loopback, Go: crypto/tls server and client in one process over
// loopback TCP, a goroutine each, with the input and output of
// mk/tls-loopback.mk's comment: the same certificates and key, TLS 1.3
// only, X25519 alone on both ends (as the Mk client offers it; Go's
// default puts the hybrid X25519MLKEM768 first) and TLS_AES_128_GCM_SHA256
// (Go's choice on a host with AES instructions), the client checking the
// chain against the root, no session tickets. Mode system: RootCAs nil,
// the system's roots (loaded once per process by Go, as by SystemPool),
// which do not hold the bench root, so each handshake is refused and
// counted. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
)

const (
	leafPEM = `-----BEGIN CERTIFICATE-----
MIIBfDCCASGgAwIBAgIBAjAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j
aCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAUMRIwEAYD
VQQDEwlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARYWAiT0sIB
PFPoIQColXkYt09CRdmx6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNK
qIH+y9bVqzPco14wXDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUH
AwEwHwYDVR0jBBgwFoAUJFRJG8q4njDrib1e2WaLjryUxnkwFAYDVR0RBA0wC4IJ
bG9jYWxob3N0MAoGCCqGSM49BAMCA0kAMEYCIQD4NROoBKH8KfhkQ/zOMONC60gR
cRjTYRvpycsxggV3OgIhAKjdsY7ozxVLBHMGzKcJDmUfhRo7ejfd4qjWiC3mdn+n
-----END CERTIFICATE-----
`
	keyPEM = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgQ3K5SvjaOAx5Gj4e
rlkGTawIlLQ2SZBirD7hp5nMGbChRANCAARYWAiT0sIBPFPoIQColXkYt09CRdmx
6LjiHDj75/IQjS3AYB3CWa+ymGXqR2ucu2cguSiCoiNKqIH+y9bVqzPc
-----END PRIVATE KEY-----
`
	rootPEM = `-----BEGIN CERTIFICATE-----
MIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAYMRYwFAYDVQQDEw1NayBiZW5j
aCByb290MCAXDTAwMDEwMTAwMDAwMFoYDzIyMDAwMTAxMDAwMDAwWjAYMRYwFAYD
VQQDEw1NayBiZW5jaCByb290MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEyTx+
YlmJmAU6O4dZ0UQ3l5NkWBp3YM0DnNEBOBQ4TupVoFHlQRki7kyY6p+VX9Qrr6Hn
z8thWyiM/gt642/2dqNCMEAwDgYDVR0PAQH/BAQDAgIEMA8GA1UdEwEB/wQFMAMB
Af8wHQYDVR0OBBYEFCRUSRvKuJ4w64m9Xtlmi468lMZ5MAoGCCqGSM49BAMCA0gA
MEUCIQCGfzbV5b0fQ7vvXGWBDlwaBxou4hsIMFapz+zbFYiqbQIgIH6Cu3lYtLDM
5NakQ34Pul2rlQOLEFZvP0CmTMKU1NE=
-----END CERTIFICATE-----
`
	piece = 16384
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tls-loopback <n> bulk|handshake|system")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	bulk := os.Args[2] == "bulk"
	cert, err := tls.X509KeyPair([]byte(leafPEM), []byte(keyPEM))
	if err != nil {
		panic(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(rootPEM))
	scfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, SessionTicketsDisabled: true,
		CurvePreferences: []tls.CurveID{tls.X25519}}
	ccfg := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.X25519}}
	system := os.Args[2] == "system"
	if system {
		ccfg.RootCAs = nil
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	count := n
	if bulk {
		count = 1
	}
	var sum uint64
	failed := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, piece)
		for i := 0; i < count; i++ {
			raw, err := l.Accept()
			if err != nil {
				failed++
				return
			}
			c := tls.Server(raw, scfg)
			if bulk {
				for {
					k, err := c.Read(buf)
					for _, b := range buf[:k] {
						sum += uint64(b)
					}
					if err == io.EOF {
						break
					}
					if err != nil {
						failed++
						break
					}
				}
			} else {
				if _, err := io.ReadFull(c, buf[:1]); err != nil {
					failed++
				} else {
					sum += uint64(buf[0])
					if _, err := c.Write(buf[:1]); err != nil {
						failed++
					}
				}
				// The client's close_notify.
				c.Read(buf[:1])
			}
			c.Close()
		}
	}()
	data := make([]byte, piece)
	for i := range data {
		data[i] = byte(7*i + 1)
	}
	var counted uint64
	for i := 0; i < count; i++ {
		c, err := tls.Dial("tcp", l.Addr().String(), ccfg)
		if err != nil && system {
			counted++
			continue
		}
		if err != nil {
			panic(err)
		}
		if bulk {
			for j := 0; j < n*64; j++ {
				if _, err := c.Write(data); err != nil {
					panic(err)
				}
				counted += piece
			}
		} else {
			if _, err := c.Write([]byte{byte(i)}); err != nil {
				panic(err)
			}
			back := make([]byte, 1)
			if _, err := io.ReadFull(c, back); err == nil {
				counted++
			}
		}
		c.Close()
	}
	<-done
	fmt.Printf("%d %d %d\n", counted, sum, failed)
}
