package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// countBody records how many bytes MaxBytesReader pulls and whether Close
// reaches the original reader.
type countBody struct {
	n      int
	read   int
	closes int
}

func (c *countBody) Read(p []byte) (int, error) {
	if c.read >= c.n {
		return 0, io.EOF
	}
	want := len(p)
	if c.read+want > c.n {
		want = c.n - c.read
	}
	for i := 0; i < want; i++ {
		p[i] = 'x'
	}
	c.read += want
	if c.read >= c.n {
		return want, io.EOF
	}
	return want, nil
}

func (c *countBody) Close() error {
	c.closes++
	return nil
}

func TestMaxBodyExactLimitPlusOneAndHuge(t *testing.T) {
	const limit int64 = 1024
	cases := []struct {
		name    string
		size    int
		wantErr bool
		wantN   int
	}{
		{name: "exact", size: int(limit), wantN: int(limit)},
		{name: "limit plus one", size: int(limit) + 1, wantErr: true, wantN: int(limit)},
		{name: "huge", size: 8 << 20, wantErr: true, wantN: int(limit)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &countBody{n: tc.size}
			var got []byte
			var readErr error
			var closesBefore int
			h := MaxBody(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, readErr = io.ReadAll(r.Body)
				closesBefore = src.closes
				if err := r.Body.Close(); err != nil {
					t.Errorf("close: %v", err)
				}
				if readErr != nil {
					http.Error(w, "too large", http.StatusRequestEntityTooLarge)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", src)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			var maxErr *http.MaxBytesError
			if tc.wantErr {
				if !errors.As(readErr, &maxErr) || maxErr.Limit != limit {
					t.Fatalf("readErr=%v", readErr)
				}
				if rec.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("status=%d", rec.Code)
				}
			} else if readErr != nil || rec.Code != http.StatusOK {
				t.Fatalf("status=%d err=%v", rec.Code, readErr)
			}
			if len(got) != tc.wantN {
				t.Fatalf("delivered %d bytes, want %d", len(got), tc.wantN)
			}
			if tc.wantErr && src.read != int(limit)+1 {
				t.Fatalf("underlying read %d, want %d", src.read, int(limit)+1)
			}
			if !tc.wantErr && src.read != int(limit) {
				t.Fatalf("underlying read %d, want %d", src.read, limit)
			}
			if closesBefore != 0 || src.closes != 1 {
				t.Fatalf("closes before handler close=%d after=%d", closesBefore, src.closes)
			}
		})
	}
}

func TestMaxBodyZeroRejectsASingleByte(t *testing.T) {
	src := &countBody{n: 1}
	var readErr error
	h := MaxBody(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}))
	req := httptest.NewRequest(http.MethodPost, "/", src)
	h.ServeHTTP(httptest.NewRecorder(), req)
	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) || maxErr.Limit != 0 {
		t.Fatalf("err=%v", readErr)
	}
	if src.read != 1 || src.closes != 1 {
		t.Fatalf("read=%d closes=%d", src.read, src.closes)
	}
}

func TestMaxBodyEmptyBodyAtZero(t *testing.T) {
	var readErr error
	h := MaxBody(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if readErr != nil || rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d err=%v", rec.Code, readErr)
	}
}
