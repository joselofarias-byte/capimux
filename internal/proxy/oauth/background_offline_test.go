package oauth

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/dbtest"
	"9router/proxy/internal/providers"
)

func TestRefreshAndRegistryHelpers(t *testing.T) {
	if _, err := Refresh(context.Background(), &Params{Provider: "missing-provider-9375"}); err == nil || !strings.Contains(err.Error(), "no OAuth refresher") {
		t.Fatalf("err=%v", err)
	}

	called := false
	Register("coverage-offline-registry", func(context.Context, *Params) (*TokenResult, error) {
		called = true
		return &TokenResult{AccessToken: "from-registry", ExpiresIn: 10}, nil
	})
	res, err := Refresh(context.Background(), &Params{Provider: "coverage-offline-registry"})
	if err != nil || !called || res.AccessToken != "from-registry" {
		t.Fatalf("res=%+v err=%v called=%v", res, err, called)
	}

	RegisterAll()
	if Get("codex") == nil {
		t.Fatal("RegisterAll did not install the codex refresher")
	}
	if Get("kimi-coding") != nil {
		t.Fatal("empty client id must stay unregistered")
	}
	if Get("claude") == nil {
		t.Fatal("custom claude refresher disappeared")
	}

	update := BuildConnectionUpdate(&TokenResult{AccessToken: "acc", RefreshToken: "ref", ExpiresIn: 120})
	if update["accessToken"] != "acc" || update["refreshToken"] != "ref" {
		t.Fatalf("update=%v", update)
	}
	exp, err := time.Parse(time.RFC3339, update["expiresAt"].(string))
	if err != nil || time.Until(exp) < time.Minute {
		t.Fatalf("expiresAt=%v err=%v", update["expiresAt"], err)
	}
	if _, ok := BuildConnectionUpdate(&TokenResult{AccessToken: "only"})["refreshToken"]; ok {
		t.Fatal("empty refresh token was stored")
	}

	if StringMap(nil) != nil || StringMap(map[string]any{}) != nil {
		t.Fatal("empty map must stay nil")
	}
	got := StringMap(map[string]any{"clientId": "cid", "blank": "", "n": 3, "region": "us-east-1"})
	if len(got) != 2 || got["clientId"] != "cid" || got["region"] != "us-east-1" {
		t.Fatalf("StringMap=%v", got)
	}
}

func TestBackgroundRefreshOffline(t *testing.T) {
	runBackgroundRefreshTick(context.Background(), nil)

	repo, cleanup := openOAuthRepo(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartBackgroundRefresh(ctx, repo)
	StartBackgroundRefresh(ctx, repo)

	var mode string
	Register("coverage-offline-bg", func(context.Context, *Params) (*TokenResult, error) {
		switch mode {
		case "ok":
			return &TokenResult{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresIn: 7200}, nil
		case "ok-no-expiry":
			return &TokenResult{AccessToken: "fresh-access"}, nil
		case "unauthorized":
			return nil, &providers.OAuthRefreshError{Status: http.StatusUnauthorized, Body: "revoked"}
		case "blip":
			return nil, &providers.OAuthRefreshError{Status: http.StatusBadGateway, Body: "blip"}
		case "timeout":
			return nil, context.DeadlineExceeded
		case "cut":
			return nil, errors.New("connection reset by peer")
		case "invalid":
			return nil, errors.New("parse response: invalid character")
		default:
			return nil, errors.New("unexpected mode")
		}
	})

	insertOAuthConn(t, repo, "bg-ok", "coverage-offline-bg")
	mode = "ok"
	runBackgroundRefreshTick(context.Background(), repo)
	data := connectionData(t, repo, "bg-ok")
	if data["accessToken"] != "fresh-access" || data["refreshToken"] != "fresh-refresh" {
		t.Fatalf("persisted data=%v", data)
	}
	if _, ok := data["oauthLockedUntil"]; ok {
		t.Fatalf("success parked the account: %v", data["oauthLockedUntil"])
	}
	deactivateAll(t, repo)

	insertOAuthConn(t, repo, "bg-unauth", "coverage-offline-bg")
	mode = "unauthorized"
	runBackgroundRefreshTick(context.Background(), repo)
	data = connectionData(t, repo, "bg-unauth")
	if _, ok := data["oauthLockedUntil"]; !ok {
		t.Fatalf("401 did not park the account: %v", data)
	}
	deactivateAll(t, repo)

	insertOAuthConn(t, repo, "bg-blip", "coverage-offline-bg")
	mode = "blip"
	runBackgroundRefreshTick(context.Background(), repo)
	data = connectionData(t, repo, "bg-blip")
	if _, ok := data["oauthLockedUntil"]; ok {
		t.Fatalf("502 parked the account: %v", data["oauthLockedUntil"])
	}
	deactivateAll(t, repo)

	for _, m := range []string{"timeout", "cut", "invalid"} {
		id := "bg-" + m
		insertOAuthConn(t, repo, id, "coverage-offline-bg")
		mode = m
		runBackgroundRefreshTick(context.Background(), repo)
		data = connectionData(t, repo, id)
		if _, ok := data["oauthLockedUntil"]; ok {
			t.Fatalf("%s parked the account", m)
		}
		if data["accessToken"] != "old-access" {
			t.Fatalf("%s wrote a token: %v", m, data["accessToken"])
		}
		deactivateAll(t, repo)
	}

	insertOAuthConn(t, repo, "bg-noexp", "coverage-offline-bg")
	mode = "ok-no-expiry"
	refreshBackgroundConnection(context.Background(), repo, ConnectionNeedingRefresh{
		ID: "bg-noexp", Provider: "coverage-offline-bg", RefreshToken: "old-refresh",
	})
	data = connectionData(t, repo, "bg-noexp")
	exp, err := time.Parse(time.RFC3339, data["expiresAt"].(string))
	if err != nil || time.Until(exp) < 30*time.Minute {
		t.Fatalf("missing expiresIn was not given a window: %v %v", data["expiresAt"], err)
	}

	closed, cleanupClosed := openOAuthRepo(t)
	cleanupClosed()
	runBackgroundRefreshTick(context.Background(), closed)
}

func openOAuthRepo(t *testing.T) (*db.Repo, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "oauth-bg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	database, err := db.OpenDatabase(f.Name())
	if err != nil {
		os.Remove(f.Name())
		t.Fatal(err)
	}
	if err := dbtest.CreateTables(database); err != nil {
		database.Close()
		os.Remove(f.Name())
		t.Fatal(err)
	}
	return db.NewRepo(database), func() {
		database.Close()
		os.Remove(f.Name())
	}
}

func deactivateAll(t *testing.T, repo *db.Repo) {
	t.Helper()
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 0`); err != nil {
		t.Fatal(err)
	}
}

func insertOAuthConn(t *testing.T, repo *db.Repo, id, provider string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"accessToken":  "old-access",
		"refreshToken": "old-refresh",
		"expiresAt":    "2000-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.RawDB().Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES (?, ?, 'oauth', 'offline', 1, 1, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, id, provider, string(raw))
	if err != nil {
		t.Fatal(err)
	}
}

func connectionData(t *testing.T, repo *db.Repo, id string) map[string]any {
	t.Helper()
	conn, err := repo.GetProviderConnectionByID(id)
	if err != nil || conn == nil {
		t.Fatalf("row %s: %v", id, err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(conn.Data), &data); err != nil {
		t.Fatal(err)
	}
	return data
}
