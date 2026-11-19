// http, Go, idiomatic std: the task of ../README.md with net/http's
// server, one goroutine per connection (GOMAXPROCS is the row's thread
// count, set by tools/bench). Usage: http <port>. Serves until killed.
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: http <port>")
		os.Exit(2)
	}
	body := []byte("Hello, World!\n")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(body)
	})
	if err := http.ListenAndServe("127.0.0.1:"+os.Args[1], nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
