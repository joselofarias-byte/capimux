package media

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

func TestRelayProjectName(t *testing.T) {
	if got := relayProjectName("  my-app  "); got != "my-app" {
		t.Fatalf("trimmed name = %q", got)
	}
	got := relayProjectName("   ")
	if !strings.HasPrefix(got, "relay-") || len(got) <= len("relay-") {
		t.Fatalf("generated name = %q", got)
	}
}

func TestPlatformRequestAndPollEdges(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/body" {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"ok":true}` || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("body request: ct=%q body=%q", r.Header.Get("Content-Type"), raw)
			}
		}
		if r.URL.Path == "/plain" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"building"}`))
	}))
	defer srv.Close()

	status, raw, err := platformRequest(t.Context(), srv.Client(), http.MethodPost, srv.URL+"/body", "", []byte(`{"ok":true}`))
	if err != nil || status != http.StatusOK || sawAuth != "" {
		t.Fatalf("no-token request: status=%d err=%v auth=%q body=%s", status, err, sawAuth, raw)
	}

	if _, _, err := platformRequest(t.Context(), srv.Client(), http.MethodGet, "://bad", "tok", nil); err == nil {
		t.Fatal("expected invalid URL to fail before a dial")
	}

	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	_, err = pollDeployStatus(ctx, srv.Client(), srv.URL+"/plain", "tok", time.Hour, 3, func(map[string]any) (bool, error) {
		calls++
		cancel()
		return false, nil
	})
	if err == nil || calls != 1 {
		t.Fatalf("cancel: calls=%d err=%v", calls, err)
	}

	if _, err := pollDeployStatus(t.Context(), srv.Client(), srv.URL+"/plain", "tok", time.Millisecond, 1, func(data map[string]any) (bool, error) {
		if len(data) != 0 {
			t.Errorf("non-200 body should not parse, got %v", data)
		}
		return false, nil
	}); err == nil || err.Error() != "deployment timed out" {
		t.Fatalf("non-200 poll: %v", err)
	}
}

func TestHandleVercelDeploy(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	ensureProxyPools(t, database)
	repo := db.NewRepo(database)

	t.Run("validation", func(t *testing.T) {
		h := newTestMediaHandler(repo)
		for _, tc := range []struct {
			name string
			body io.Reader
			want int
		}{
			{"read error", errReader{}, http.StatusBadRequest},
			{"bad json", strings.NewReader("{"), http.StatusBadRequest},
			{"missing token", strings.NewReader(`{"projectName":"app"}`), http.StatusBadRequest},
		} {
			rec := httptest.NewRecorder()
			h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", tc.body))
			if rec.Code != tc.want {
				t.Errorf("%s: status %d body %s", tc.name, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("upstream message and empty message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			if strings.Contains(r.Header.Get("Authorization"), "known") {
				_, _ = w.Write([]byte(`{"error":{"message":"token rejected"}}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		h := newTestMediaHandler(repo)
		client, hosts := newLoopbackClient(srv, srv.URL)
		h.Client = client
		rec := httptest.NewRecorder()
		h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", strings.NewReader(`{"vercelToken":"known","projectName":"app"}`)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "token rejected") {
			t.Fatalf("message: %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", strings.NewReader(`{"vercelToken":"other"}`)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Failed to create Vercel deployment") {
			t.Fatalf("empty message: %d %s", rec.Code, rec.Body.String())
		}
		for _, host := range hosts.hosts {
			if host != "api.vercel.com" {
				t.Fatalf("vercel deploy dialed %q", host)
			}
		}
	})

	t.Run("ready deployment is stored as a vercel pool", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v13/deployments"):
				_, _ = w.Write([]byte(`{"uid":"dep-uid","url":"my-app.vercel.app"}`))
			case r.Method == http.MethodPatch:
				w.WriteHeader(http.StatusOK)
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(`{"readyState":"READY","url":"my-app.vercel.app"}`))
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()
		h := newTestMediaHandler(repo)
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", strings.NewReader(`{"vercelToken":"tok","projectName":"  my-app  "}`)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["deployUrl"] != "https://my-app.vercel.app" {
			t.Fatalf("deployUrl = %v", body["deployUrl"])
		}
		pool, _ := body["proxyPool"].(map[string]any)
		if pool["type"] != "vercel" || pool["strictProxy"] != false || pool["noProxy"] != "" || pool["name"] != "my-app" {
			t.Fatalf("pool = %#v", pool)
		}
	})

	t.Run("deployment error state", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"dep1","projectId":"proj1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"readyState":"ERROR"}`))
		}))
		defer srv.Close()
		h := newTestMediaHandler(repo)
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", strings.NewReader(`{"vercelToken":"tok","projectName":"app"}`)))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "ERROR") {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("dial failure stays on the platform host", func(t *testing.T) {
		h := newTestMediaHandler(repo)
		var hosts []string
		h.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hosts = append(hosts, r.URL.Host)
			return nil, io.ErrClosedPipe
		})}
		rec := httptest.NewRecorder()
		h.HandleVercelDeploy(rec, httptest.NewRequest(http.MethodPost, "/vercel", strings.NewReader(`{"vercelToken":"tok"}`)))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if len(hosts) != 1 || hosts[0] != "api.vercel.com" {
			t.Fatalf("hosts = %v", hosts)
		}
	})
}

func TestHandleDenoDeploy(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	ensureProxyPools(t, database)
	repo := db.NewRepo(database)
	h := newTestMediaHandler(repo)

	t.Run("validation", func(t *testing.T) {
		for _, tc := range []struct {
			body string
			read bool
		}{
			{body: "{", read: false},
			{body: `{"denoToken":"tok"}`, read: false},
			{body: `{"orgDomain":"acme.deno.dev"}`, read: false},
		} {
			rec := httptest.NewRecorder()
			h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %s: %d %s", tc.body, rec.Code, rec.Body.String())
			}
		}
		rec := httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", errReader{}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("read: %d", rec.Code)
		}
	})

	t.Run("conflict and create failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.Header.Get("Authorization"), "conflict") {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(`nope`))
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(`{"denoToken":"conflict","orgDomain":"acme.deno.dev","projectName":"app"}`)))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already exists") {
			t.Fatalf("conflict: %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(`{"denoToken":"other","orgDomain":"acme.deno.dev","projectName":"app"}`)))
		if rec.Code != http.StatusTeapot || !strings.Contains(rec.Body.String(), "nope") {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("deploy failure deletes the app", func(t *testing.T) {
		var deleted bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apps"):
				_, _ = w.Write([]byte(`{"id":"app1"}`))
			case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/deploy"):
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`boom`))
			case r.Method == http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(`{"denoToken":"tok","orgDomain":"acme.deno.dev","projectName":"app"}`)))
		if rec.Code != http.StatusBadGateway || !deleted || !strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("deleted=%v status=%d body=%s", deleted, rec.Code, rec.Body.String())
		}
	})

	t.Run("revision poll error deletes the app", func(t *testing.T) {
		var deleted bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apps"):
				_, _ = w.Write([]byte(`{"id":"app1"}`))
			case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/deploy"):
				_, _ = w.Write([]byte(`{"id":"rev1"}`))
			case r.Method == http.MethodGet:
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("no hijacker")
				}
				conn, _, err := hj.Hijack()
				if err != nil {
					t.Fatal(err)
				}
				conn.Close()
			case r.Method == http.MethodDelete:
				deleted = true
			}
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(`{"denoToken":"tok","orgDomain":"acme.deno.dev"}`)))
		if rec.Code != http.StatusInternalServerError || !deleted {
			t.Fatalf("deleted=%v status=%d body=%s", deleted, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
			pool, _ := body["proxyPool"].(map[string]any)
			if pool != nil {
				t.Fatalf("poll failure stored a pool: %#v", pool)
			}
		}
	})

	t.Run("succeeded revision stores a deno pool", func(t *testing.T) {
		var hosts []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apps"):
				_, _ = w.Write([]byte(`{"id":"app1"}`))
			case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/deploy"):
				_, _ = w.Write([]byte(`{"id":"rev1"}`))
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(`{"status":"succeeded"}`))
			}
		}))
		defer srv.Close()
		client, rt := newLoopbackClient(srv, srv.URL)
		h.Client = client
		rec := httptest.NewRecorder()
		h.HandleDenoDeploy(rec, httptest.NewRequest(http.MethodPost, "/deno", strings.NewReader(`{"denoToken":"tok","orgDomain":"acme.deno.dev","projectName":"relay-app"}`)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["deployUrl"] != "https://relay-app.acme.deno.net" {
			t.Fatalf("deployUrl = %v", body["deployUrl"])
		}
		pool, _ := body["proxyPool"].(map[string]any)
		if pool["type"] != "deno" || pool["strictProxy"] != false {
			t.Fatalf("pool = %#v", pool)
		}
		hosts = rt.hosts
		for _, host := range hosts {
			if host != "api.deno.com" {
				t.Fatalf("deno deploy dialed %q", host)
			}
		}
	})
}

func TestHandleCloudflareDeploy(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	ensureProxyPools(t, database)
	repo := db.NewRepo(database)
	h := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("json: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body.String())
	}

	t.Run("upload error uses the platform message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method %s", r.Method)
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Errorf("content-type %s", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":[{"message":"bad token"}]}`))
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct","apiToken":"tok","projectName":"worker"}`)))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "bad token") {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("upload error without a message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct","apiToken":"tok"}`)))
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Failed to upload Worker") {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing workers.dev subdomain does not store a pool", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"success":true}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":{}}`))
		}))
		defer srv.Close()
		h.Client, _ = newLoopbackClient(srv, srv.URL)
		rec := httptest.NewRecorder()
		h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct","apiToken":"tok","projectName":"worker"}`)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "workers.dev") {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("subdomain is stored as a cloudflare pool", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPut:
				w.WriteHeader(http.StatusOK)
			case http.MethodPost:
				w.WriteHeader(http.StatusOK)
			case http.MethodGet:
				_, _ = w.Write([]byte(`{"result":{"subdomain":"example"}}`))
			}
		}))
		defer srv.Close()
		client, rt := newLoopbackClient(srv, srv.URL)
		h.Client = client
		rec := httptest.NewRecorder()
		h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct","apiToken":"tok","projectName":"worker"}`)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["deployUrl"] != "https://worker.example.workers.dev" {
			t.Fatalf("deployUrl = %v", body["deployUrl"])
		}
		pool, _ := body["proxyPool"].(map[string]any)
		if pool["type"] != "cloudflare" || pool["strictProxy"] != false || pool["proxyUrl"] != "https://worker.example.workers.dev" {
			t.Fatalf("pool = %#v", pool)
		}
		for _, host := range rt.hosts {
			if host != "api.cloudflare.com" {
				t.Fatalf("cloudflare deploy dialed %q", host)
			}
		}
	})

	t.Run("dial failure does not open a second host", func(t *testing.T) {
		var hosts []string
		h.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hosts = append(hosts, r.URL.Host)
			return nil, io.ErrClosedPipe
		})}
		rec := httptest.NewRecorder()
		h.HandleCloudflareDeploy(rec, httptest.NewRequest(http.MethodPost, "/cf", strings.NewReader(`{"accountId":"acct","apiToken":"tok"}`)))
		if rec.Code != http.StatusBadGateway || len(hosts) != 1 || hosts[0] != "api.cloudflare.com" {
			t.Fatalf("status %d hosts %v body %s", rec.Code, hosts, rec.Body.String())
		}
	})
}
