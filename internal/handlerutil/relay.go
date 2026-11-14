package handlerutil

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// upstreamReadFailedMessage is the only text a failed 2xx body read shows
// the client. It is fixed so a url.Error, query string, or header line from
// the transport cannot leak into the response or into a log of this error.
const upstreamReadFailedMessage = "failed to read upstream response"

// ErrUpstreamRead is returned after RelayUpstream has already written 502 JSON
// for a 2xx body that could not be read. The text is upstreamReadFailedMessage.
var ErrUpstreamRead = errors.New(upstreamReadFailedMessage)

// RelayUpstream copies an upstream status and body to w.
//
// A 2xx whose first read fails with anything other than EOF is answered with
// 502 JSON and ErrUpstreamRead, and applyHeaders is not called, so upstream
// headers are not committed. A clean EOF with no bytes is a real empty 2xx
// and is forwarded as that status. Any other status is forwarded unchanged,
// including when the body cannot be read: the upstream status is what a
// fallback decision already depends on.
func RelayUpstream(w http.ResponseWriter, status int, body io.Reader, applyHeaders func()) error {
	src := body
	if status >= 200 && status < 300 {
		peeked, err := peekUpstream(body)
		if err != nil {
			WriteJSONError(w, http.StatusBadGateway, upstreamReadFailedMessage)
			return ErrUpstreamRead
		}
		src = peeked
	}
	if applyHeaders != nil {
		applyHeaders()
	}
	w.WriteHeader(status)
	if src != nil {
		_, _ = io.Copy(w, src)
	}
	return nil
}

// peekUpstream returns a reader that replays the first byte, or an empty
// reader on a clean EOF. A read error with no bytes is returned as-is so the
// caller can replace it with ErrUpstreamRead and drop the original text.
func peekUpstream(body io.Reader) (io.Reader, error) {
	if body == nil {
		return bytes.NewReader(nil), nil
	}
	var buf [1]byte
	for {
		n, err := body.Read(buf[:])
		if n > 0 {
			first := []byte{buf[0]}
			return io.MultiReader(bytes.NewReader(first), body), nil
		}
		if errors.Is(err, io.EOF) {
			return bytes.NewReader(nil), nil
		}
		if err != nil {
			return nil, err
		}
	}
}
