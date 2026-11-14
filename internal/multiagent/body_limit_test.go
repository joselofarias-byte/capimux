package multiagent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// limitBody counts reads and closes for the 1 MiB MaxBytesReader in decodeRequest.
type limitBody struct {
	payload string
	off     int
	read    int
	closes  int
}

func (b *limitBody) Read(p []byte) (int, error) {
	if b.off >= len(b.payload) {
		return 0, io.EOF
	}
	n := copy(p, b.payload[b.off:])
	b.off += n
	b.read += n
	if b.off >= len(b.payload) {
		return n, io.EOF
	}
	return n, nil
}

func (b *limitBody) Close() error {
	b.closes++
	return nil
}

func TestDecodeRequestBodyLimit(t *testing.T) {
	const limit = 1 << 20
	base := `{"prompt":"hi","models":["approved/model"],"dry_run":true}`
	if len(base) >= limit {
		t.Fatal("fixture larger than the limit")
	}
	t.Setenv("CAPIMUX_MULTIAGENT_ALLOWED_MODELS", "approved/model")
	h := Handler(func(context.Context, string, string) (string, error) {
		t.Fatal("body-limit request invoked a provider")
		return "", nil
	})

	t.Run("exact", func(t *testing.T) {
		body := &limitBody{payload: base + strings.Repeat(" ", limit-len(base))}
		req := httptest.NewRequest(http.MethodPost, "/api/multiagent/run", body)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if body.read != limit {
			t.Fatalf("read %d, want %d", body.read, limit)
		}
		if err := req.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if body.closes != 1 {
			t.Fatalf("closes=%d", body.closes)
		}
	})

	t.Run("limit plus one", func(t *testing.T) {
		body := &limitBody{payload: base + strings.Repeat(" ", limit-len(base)+1)}
		req := httptest.NewRequest(http.MethodPost, "/api/multiagent/run", body)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if body.read != limit+1 {
			t.Fatalf("read %d, want %d", body.read, limit+1)
		}
		_ = req.Body.Close()
		if body.closes != 1 {
			t.Fatalf("closes=%d", body.closes)
		}
	})

	t.Run("huge", func(t *testing.T) {
		// Spaces stay valid JSON whitespace, so the decoder keeps reading
		// until MaxBytesReader stops it instead of failing on the first extra byte.
		body := &limitBody{payload: base + strings.Repeat(" ", (8<<20)-len(base))}
		req := httptest.NewRequest(http.MethodPost, "/api/multiagent/run", body)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if body.read != limit+1 {
			t.Fatalf("read %d, want the cap %d and not the whole body", body.read, limit+1)
		}
		_ = req.Body.Close()
		if body.closes != 1 {
			t.Fatalf("closes=%d", body.closes)
		}
	})
}
