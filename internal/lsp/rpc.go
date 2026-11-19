// Why: LSP's base protocol is the framing every message travels in.
//
// What: readFrame, the message type, the writer with reply, fail and notify, and
// the JSON-RPC error codes.
//
// How: frames by Content-Length, one message per frame, and the error codes of
// the specification; the writer serialises concurrent writers.

// Package lsp is `mk lsp`: a Language Server Protocol 3.17 server over
// standard input and output. It answers from the same
// stages, cache and query code as `mk check` and `mk query`.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// JSON-RPC and LSP error codes (LSP 3.17, "Response Message").
const (
	codeParseError           = -32700
	codeInvalidRequest       = -32600
	codeMethodNotFound       = -32601
	codeInvalidParams        = -32602
	codeInternalError        = -32603
	codeServerNotInitialized = -32002
	codeRequestCancelled     = -32800
	codeRequestFailed        = -32803
)

// maxFrame bounds one message body: an editor never sends a buffer near
// this, and a bad header must not make the server allocate without bound.
const maxFrame = 1 << 30

// message is any JSON-RPC message as read: a request has ID and Method, a
// notification only Method, a response ID and Result or Error.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// isRequest reports whether the message expects a response.
func (m *message) isRequest() bool { return len(m.ID) > 0 && string(m.ID) != "null" && m.Method != "" }

// rpcError is a response error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return strconv.Itoa(e.Code) + ": " + e.Message }

// errFraming is a broken stream: the server cannot find the next message.
var errFraming = errors.New("lsp: broken framing")

// readFrame reads one message body. io.EOF at a frame boundary is a clean
// end; anything else that breaks the frame wraps errFraming.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	first := true
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && first && line == "" {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("%w: header: %v", errFraming, err)
		}
		first = false
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%w: header line %q", errFraming, line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 || n > maxFrame {
				return nil, fmt.Errorf("%w: Content-Length %q", errFraming, value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("%w: no Content-Length", errFraming)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("%w: body: %v", errFraming, err)
	}
	return body, nil
}

// writer frames and writes messages; safe from any goroutine.
type writer struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func (w *writer) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if _, err := io.WriteString(w.w, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"); err != nil {
		w.err = err
		return err
	}
	if _, err := w.w.Write(body); err != nil {
		w.err = err
		return err
	}
	return nil
}

// response is an outgoing response; Result is always present on success
// (null included), Error on failure.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *rpcError       `json:"error"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func (w *writer) reply(id json.RawMessage, result any) error {
	return w.send(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (w *writer) fail(id json.RawMessage, code int, msg string) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return w.send(errorResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (w *writer) notify(method string, params any) error {
	return w.send(notification{JSONRPC: "2.0", Method: method, Params: params})
}
