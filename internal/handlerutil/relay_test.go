package handlerutil

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayUpstreamReadErrorIsFixedJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	headersApplied := false
	err := RelayUpstream(rec, http.StatusOK, errReader{err: errors.New("read http://user:capimux-userinfo-9f3a@127.0.0.1/?api_key=capimux-query-9f3a")}, func() {
		headersApplied = true
		rec.Header().Set("X-Api-Key", "capimux-header-9f3a")
	})
	if !errors.Is(err, ErrUpstreamRead) {
		t.Fatalf("err %v", err)
	}
	if headersApplied {
		t.Fatal("upstream headers were applied")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"bad_gateway"`, `"type":"server_error"`, `"message":"failed to read upstream response"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %s", body, want)
		}
	}
	for _, token := range []string{"capimux-userinfo-9f3a", "capimux-query-9f3a", "capimux-header-9f3a"} {
		if strings.Contains(body, token) || strings.Contains(err.Error(), token) {
			t.Fatalf("fixture material leaked")
		}
	}
	if rec.Header().Get("X-Api-Key") != "" {
		t.Fatal("secret header forwarded")
	}
}

func TestRelayUpstreamEmpty2xxStaysEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	err := RelayUpstream(rec, http.StatusOK, strings.NewReader(""), func() {
		rec.Header().Set("Content-Type", "application/json")
	})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content-type %q", rec.Header().Get("Content-Type"))
	}
}

func TestRelayUpstreamKeepsFirstByte(t *testing.T) {
	rec := httptest.NewRecorder()
	err := RelayUpstream(rec, http.StatusOK, strings.NewReader(`{"ok":true}`), nil)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestRelayUpstreamNon2xxKeepsStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	err := RelayUpstream(rec, http.StatusTooManyRequests, errReader{err: errors.New("read http://user:capimux-userinfo-9f3a@127.0.0.1/")}, func() {
		rec.Header().Set("Content-Type", "application/json")
	})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
func (e errReader) Close() error             { return nil }

var _ io.Reader = errReader{}
