package oauth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fixedTransport answers every request in memory: no network is used, so the
// real token endpoints are never contacted.
type fixedTransport struct {
	body  []byte
	hosts []string
}

func (f *fixedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.hosts = append(f.hosts, r.URL.Host)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(f.body)),
		Request:    r,
	}, nil
}

func oversizedTokenBody() []byte {
	// Valid JSON prefix padded past 1 MiB: without the limit it would be read
	// whole into memory and parsed.
	pad := strings.Repeat("a", (1<<20)+16)
	return []byte(`{"access_token":"t","refresh_token":"r","expires_in":3600,"pad":"` + pad + `"}`)
}

func smallTokenBody() []byte {
	return []byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
}

func refreshersUnderTest() map[string]func(context.Context, *http.Client) (*TokenResult, error) {
	return map[string]func(context.Context, *http.Client) (*TokenResult, error){
		"claude": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return refreshClaude(ctx, &Params{Client: c, Provider: "claude", RefreshToken: "test-refresh"})
		},
		"xai": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return refreshXAI(ctx, &Params{Client: c, Provider: "xai", RefreshToken: "test-refresh"})
		},
		"standard": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return doFormRefresh(ctx, c, "https://token.example.test/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"test-refresh"}})
		},
	}
}

func TestOAuthRefreshRejectsOversizedTokenResponse(t *testing.T) {
	for name, refresh := range refreshersUnderTest() {
		t.Run(name, func(t *testing.T) {
			rt := &fixedTransport{body: oversizedTokenBody()}
			res, err := refresh(context.Background(), &http.Client{Transport: rt})
			if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
				t.Fatalf("expected size-limit error, got res=%v err=%v", res, err)
			}
			if strings.Contains(err.Error(), "aaaa") {
				t.Fatal("error must not echo the oversized body")
			}
			if len(rt.hosts) != 1 {
				t.Fatalf("expected exactly one in-memory request, got %v", rt.hosts)
			}
		})
	}
}

func TestOAuthRefreshAcceptsNormalTokenResponse(t *testing.T) {
	for name, refresh := range refreshersUnderTest() {
		t.Run(name, func(t *testing.T) {
			res, err := refresh(context.Background(), &http.Client{Transport: &fixedTransport{body: smallTokenBody()}})
			if err != nil {
				t.Fatalf("normal token response rejected: %v", err)
			}
			if res == nil || res.AccessToken != "new-access" {
				t.Fatalf("unexpected result: %#v", res)
			}
		})
	}
}
