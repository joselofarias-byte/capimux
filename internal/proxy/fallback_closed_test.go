package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// strictTripper stands in for the chat package's failingRoundTripper: every
// request fails and the type is not an *http.Transport, so a proxy-shaped
// error must not be replayed on a direct connection.
type strictTripper struct {
	hits int
}

func (s *strictTripper) RoundTrip(*http.Request) (*http.Response, error) {
	s.hits++
	return nil, errors.New("strict proxy pool is unavailable")
}

func TestFallbackTransportStrictErrorDoesNotEgressDirect(t *testing.T) {
	var directHits int
	directSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer directSrv.Close()

	base := &strictTripper{}
	direct := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		directHits++
		return nil, errors.New("direct leg blocked")
	})}
	ft := &FallbackTransport{Base: base, Direct: direct.Transport}
	req, err := http.NewRequest(http.MethodPost, directSrv.URL+"/v1/chat", strings.NewReader(`{"secret":"not-a-credential"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ft.RoundTrip(req)
	if resp != nil || err == nil || !strings.Contains(err.Error(), "strict proxy") {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	if base.hits != 1 || directHits != 0 {
		t.Fatalf("base=%d direct=%d", base.hits, directHits)
	}
}

func TestFallbackTransportProxyRefusalUsesDirectLegOnly(t *testing.T) {
	var directHits int
	directSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct-ok"))
	}))
	defer directSrv.Close()

	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("blocked-by-allowlist"))
	}))
	defer proxySrv.Close()
	proxyURL, err := url.Parse(proxySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	directRT := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		directHits++
		return http.DefaultTransport.RoundTrip(r)
	})
	ft := &FallbackTransport{Base: base, Direct: directRT}
	req, err := http.NewRequest(http.MethodGet, directSrv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ft.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "direct-ok" || directHits != 1 {
		t.Fatalf("body=%s directHits=%d", body, directHits)
	}
}

func TestNewFallbackTransportNilBase(t *testing.T) {
	rt := NewFallbackTransport(nil)
	ft, ok := rt.(*FallbackTransport)
	if !ok || ft.Base == nil || ft.Direct == nil {
		t.Fatalf("transport=%T %#v", rt, rt)
	}
}

func TestFallbackTransportNonProxy500DoesNotReplay(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	var directHits int
	ft := &FallbackTransport{
		Base: http.DefaultTransport,
		Direct: roundTripFunc(func(*http.Request) (*http.Response, error) {
			directHits++
			return nil, errors.New("direct leg blocked")
		}),
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ft.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	resp.Body.Close()
	if hits != 1 || directHits != 0 {
		t.Fatalf("hits=%d direct=%d", hits, directHits)
	}
}
