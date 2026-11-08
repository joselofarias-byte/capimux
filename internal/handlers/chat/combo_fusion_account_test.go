package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/controlplane/registry"
	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestFusionPanel_DefaultKeyProviderUsesConfiguredAccountFeedback(t *testing.T) {
	const model = "audit-fusion-model"
	const connID = "conn-opencode-fusion"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"fusion route denied","type":"FreeTierError"}}`))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE provider = 'opencode'`); err != nil {
		t.Fatalf("clear opencode connections: %v", err)
	}
	seedConnDB(t, database, "opencode", connID, "", upstream.URL)

	orig, ok := providers.KnownProviders["opencode"]
	if !ok {
		t.Fatal("opencode provider config missing")
	}
	cfg := orig
	cfg.BaseURL = upstream.URL
	providers.KnownProviders["opencode"] = cfg
	defer func() { providers.KnownProviders["opencode"] = orig }()

	if err := registry.InitRegistry(nil); err != nil {
		t.Fatalf("init registry: %v", err)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	result := h.makePanelCall(
		[]byte(`{"model":"ignored","messages":[{"role":"user","content":"hi"}],"stream":false}`),
		"opencode/"+model,
	)(context.Background())
	if result == nil || result.err == nil {
		t.Fatalf("expected fusion panel upstream error, got %#v", result)
	}
	var ue *upstreamError
	if !errors.As(result.err, &ue) || ue.StatusCode != http.StatusForbidden {
		t.Fatalf("expected upstream 403 from the configured account, got %v", result.err)
	}

	// Cooldown on the concrete account is the feedback this tree records.
	// main does not feed globalTrustManager from the data plane, so in-memory
	// quarantine (the extra assertion on the #105 stack) is not expected here.
	locked, err := repo.IsConnectionModelLocked(connID, model)
	if err != nil {
		t.Fatalf("IsConnectionModelLocked: %v", err)
	}
	if !locked {
		t.Fatal("expected concrete fusion account/model lock after retryable 403")
	}
	for _, synthetic := range []string{"default", "noauth"} {
		syntheticLocked, lockErr := repo.IsConnectionModelLocked(synthetic, model)
		if lockErr != nil {
			t.Fatalf("IsConnectionModelLocked(%s): %v", synthetic, lockErr)
		}
		if syntheticLocked {
			t.Fatalf("synthetic %s id was locked; cooldown must stay on the configured account", synthetic)
		}
	}
}
