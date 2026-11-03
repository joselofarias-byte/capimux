package entitlements

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPTransportDoesNotFollowRedirects(t *testing.T) {
	var target atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	transport, err := NewHTTPTransport(origin.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Activate(context.Background(), ActivationRequest{ActivationCode: "SECRET-INVITE"})
	if err == nil {
		t.Fatal("redirected activation unexpectedly succeeded")
	}
	if !errors.Is(err, ErrControlPlaneRedirect) {
		t.Fatalf("redirect error = %v, want ErrControlPlaneRedirect", err)
	}
	if strings.Contains(err.Error(), "SECRET-INVITE") {
		t.Fatalf("redirect error leaks activation code: %v", err)
	}
	if err := transport.Release(context.Background(), ReleaseRequest{}); err == nil {
		t.Fatal("redirected release unexpectedly succeeded")
	}
	if got := target.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
}

func TestHTTPTransportAlwaysHasBoundedTimeoutAndDoesNotMutateCallerClient(t *testing.T) {
	transport, err := NewHTTPTransport("https://license.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if transport.client.Timeout <= 0 || transport.client.Timeout > 30*time.Second {
		t.Fatalf("default timeout = %s, want bounded", transport.client.Timeout)
	}

	caller := &http.Client{}
	transport, err = NewHTTPTransport("https://license.example.test", caller)
	if err != nil {
		t.Fatal(err)
	}
	if transport.client.Timeout != defaultControlPlaneTimeout {
		t.Fatalf("zero caller timeout became %s, want default", transport.client.Timeout)
	}
	if caller.Timeout != 0 || caller.CheckRedirect != nil {
		t.Fatal("NewHTTPTransport mutated the caller's http.Client")
	}
}

func TestHTTPTransportServerDownReturnsError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	baseURL := server.URL
	server.Close()

	transport, err := NewHTTPTransport(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Renew(context.Background(), RenewalRequest{}); err == nil {
		t.Fatal("renew against a down server unexpectedly succeeded")
	}
}

func TestHTTPTransportHungServerRespectsDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	transport, err := NewHTTPTransport(server.URL, &http.Client{Timeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := transport.Renew(context.Background(), RenewalRequest{}); err == nil {
		t.Fatal("renew against a hung server unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("hung server blocked renew for %s", elapsed)
	}
}

// An existing installation with a valid cached lease must keep its Pro state
// when the license server is down or slow, until the signed lease itself
// expires. After final expiry it falls back to Community, never to a lockout.
func TestCachedLeaseSurvivesServerOutageUntilExpiry(t *testing.T) {
	signer := newClientTestSigner(t)
	store := NewRuntimeStore(t.TempDir())
	identity, err := store.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lease := signer.signLease(t, identity.ID, "lic-outage-1", "nonce-1", nil)
	if _, err := store.AdvanceTrustedTime(signer.now); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLease(lease); err != nil {
		t.Fatal(err)
	}

	unblock := make(chan struct{})
	for name, handler := range map[string]http.HandlerFunc{
		"http-503": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"timeout": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			defer func() {
				select {
				case <-unblock:
				default:
					if name == "timeout" {
						close(unblock)
					}
				}
			}()
			transport, err := NewHTTPTransport(server.URL, &http.Client{Timeout: 150 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(ClientOptions{
				Store:     store,
				Transport: transport,
				Keys:      signer.keys(),
				Build:     signer.build,
				Now:       func() time.Time { return signer.now.Add(time.Hour) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Renew(context.Background()); err == nil {
				t.Fatal("renew during outage unexpectedly succeeded")
			}
			cached, err := store.LoadLease()
			if err != nil || string(cached) != string(lease) {
				t.Fatalf("outage changed the cached lease (err=%v)", err)
			}
		})
	}

	state, err := store.LoadRuntime(RuntimeOptions{
		Keys:  signer.keys(),
		Build: signer.build,
		Now:   func() time.Time { return signer.now.Add(2 * time.Hour) },
	})
	if err != nil {
		t.Fatalf("load runtime after outage: %v", err)
	}
	if state.Provider.Status().Mode != "licensed" || !state.Provider.Enabled(CapabilityAdvancedRouting) {
		t.Fatalf("valid cached lease lost Pro during outage: %+v", state.Provider.Status())
	}

	expired, err := store.LoadRuntime(RuntimeOptions{
		Keys:  signer.keys(),
		Build: signer.build,
		Now:   func() time.Time { return signer.now.Add(11 * 24 * time.Hour) },
	})
	if expired == nil || expired.Provider == nil {
		t.Fatalf("expired lease returned nil runtime (err=%v)", err)
	}
	if expired.Provider.Status().Mode == "licensed" || expired.Provider.Enabled(CapabilityAdvancedRouting) {
		t.Fatalf("expired lease still enables Pro: %+v", expired.Provider.Status())
	}
}

// Without any lease the runtime is Community: no Pro capability is ever enabled
// by the absence of configuration or of a license server.
func TestAbsentLicenseNeverEnablesPro(t *testing.T) {
	store := NewRuntimeStore(t.TempDir())
	state, err := store.LoadRuntime(RuntimeOptions{Now: time.Now})
	if err != nil {
		t.Fatalf("community startup: %v", err)
	}
	for _, capability := range []Capability{
		CapabilityAdvancedRouting, CapabilityMultiAccountPolicy, CapabilityIntentProfiles,
		CapabilityAdvancedTelemetry, CapabilityBudgetPolicy, CapabilityQuotaExpiry,
	} {
		if state.Provider.Enabled(capability) {
			t.Fatalf("capability %s enabled without a license", capability)
		}
	}
	if mode := state.Provider.Status().Mode; mode != "community" {
		t.Fatalf("mode without license = %q, want community", mode)
	}
}

func TestActivationNeverSendsInstallationPrivateKey(t *testing.T) {
	signer := newClientTestSigner(t)
	store := NewRuntimeStore(t.TempDir())
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body.Store(string(raw))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	transport, err := NewHTTPTransport(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientOptions{
		Store:     store,
		Transport: transport,
		Keys:      signer.keys(),
		Build:     signer.build,
		Now:       func() time.Time { return signer.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Activate(context.Background(), "INVITE-TEST"); err == nil {
		t.Fatal("activation against a 403 server unexpectedly succeeded")
	}

	identity, err := store.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	seedFile, err := os.ReadFile(filepath.Join(store.Dir(), installationKeyName))
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := body.Load().(string)
	if sent == "" {
		t.Fatal("server received no activation request")
	}
	for name, secret := range map[string]string{
		"seed file":          strings.TrimSpace(string(seedFile)),
		"private key b64":    base64.StdEncoding.EncodeToString(identity.PrivateKey),
		"private key b64url": base64.RawURLEncoding.EncodeToString(identity.PrivateKey.Seed()),
	} {
		if strings.Contains(sent, secret) {
			t.Fatalf("activation request contains the installation %s", name)
		}
	}
	if !strings.Contains(sent, base64.StdEncoding.EncodeToString(identity.PublicKey)) {
		t.Fatal("activation request does not carry the installation public key")
	}
}

func TestRuntimeStoreUsesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	store := NewRuntimeStore(t.TempDir())
	if _, err := store.InstallationIdentity(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, store.Dir(), privateDirPerm)
	assertMode(t, filepath.Join(store.Dir(), installationKeyName), privateFilePerm)
	assertMode(t, filepath.Join(store.Dir(), installationIDName), privateFilePerm)
}

// Installs created by older builds may have a 0755 directory and a 0644 key.
// Loading them must keep the same identity and tighten the permissions.
func TestRuntimeStoreTightensLegacyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	store := NewRuntimeStore(t.TempDir())
	first, err := store.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(store.Dir(), installationKeyName)
	if err := os.Chmod(store.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}

	again, err := NewRuntimeStore(filepath.Dir(store.Dir())).InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("legacy reload changed installation id: %q -> %q", first.ID, again.ID)
	}
	assertMode(t, store.Dir(), privateDirPerm)
	assertMode(t, keyPath, privateFilePerm)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}
