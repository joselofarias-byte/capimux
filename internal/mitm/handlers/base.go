package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"9router/proxy/internal/log"
)

var proxyBase = "http://localhost:20130"

// FetchRouter sends a request body to the 9router proxy endpoint and returns the response.
func FetchRouter(ctx context.Context, body []byte, endpoint string, headers http.Header, apiKey string) (*http.Response, error) {
	url := proxyBase + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// Forward original headers
	if auth := headers.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if ct := headers.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if ua := headers.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	return client.Do(req)
}

// PipeSSE copies streaming SSE chunks from upstream to the mitm client response.
func PipeSSE(upstream *http.Response, w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(upstream.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	for {
		n, err := upstream.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write SSE chunk: %w", werr)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}

const upstreamTruncatedMsg = "upstream response truncated"

// upstreamPipeError is a failed JSON copy. wroteHeader is true when the
// status line already reached the client, so a 502 can no longer be sent.
type upstreamPipeError struct {
	err         error
	wroteHeader bool
}

func (e *upstreamPipeError) Error() string {
	if e == nil || e.err == nil {
		return upstreamTruncatedMsg
	}
	return e.err.Error()
}

func (e *upstreamPipeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// PipeJSON copies a non-streaming JSON response from upstream to the mitm client.
// The body is read in full before any status line is written. A short read
// returns an error and leaves the client response unstarted.
func PipeJSON(upstream *http.Response, w http.ResponseWriter) error {
	body, err := io.ReadAll(upstream.Body)
	if err != nil {
		return &upstreamPipeError{err: err, wroteHeader: headerWriterCommitted(w)}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(upstream.StatusCode)
	if _, werr := w.Write(body); werr != nil {
		return &upstreamPipeError{err: fmt.Errorf("write JSON response: %w", werr), wroteHeader: true}
	}
	return nil
}

// pipeUpstream forwards an upstream response. JSON is buffered first.
// Streaming SSE keeps its existing prefix-forwarding behavior.
func pipeUpstream(w http.ResponseWriter, upstream *http.Response, stream bool) {
	if stream {
		PipeSSE(upstream, w)
		return
	}
	if err := PipeJSON(upstream, w); err != nil {
		failJSONPipe(w, err)
	}
}

func failJSONPipe(w http.ResponseWriter, err error) {
	if jsonHeadersSent(err) {
		// Log the read error only. Do not attach headers or the body:
		// both can carry bearer tokens and credentials.
		log.Error("mitm", "upstream JSON truncated after headers were sent; closing connection", "error", err)
		abortClient(w)
		return
	}
	SendError(w, http.StatusBadGateway, upstreamTruncatedMsg)
}

func jsonHeadersSent(err error) bool {
	var pe *upstreamPipeError
	return errors.As(err, &pe) && pe.wroteHeader
}

type headerCommitter interface {
	WroteHeader() bool
}

func headerWriterCommitted(w http.ResponseWriter) bool {
	c, ok := w.(headerCommitter)
	return ok && c.WroteHeader()
}

type connAborter interface {
	Abort()
}

// abortClient cuts a response whose status line is already on the wire.
// A clean close would make a missing Content-Length look like a finished
// body, so the MITM writer resets the TCP connection. net/http.Server
// still gets ErrAbortHandler when the writer cannot abort itself.
func abortClient(w http.ResponseWriter) {
	if a, ok := w.(connAborter); ok {
		a.Abort()
		return
	}
	if hj, ok := w.(http.Hijacker); ok {
		conn, _, err := hj.Hijack()
		if err == nil && conn != nil {
			_ = conn.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

// SendError writes a JSON error response to the mitm client.
func SendError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(fmt.Sprintf(`{"error":{"message":"%s","type":"mitm_error","code":%d}}`, msg, status)))
}
