package handlers

import (
	"net/http"
	"strings"
	"testing"
)

// A client API key must not read or write Google Drive backups, even if it
// also presents the dashboard password. The production router (not the
// handler-only table) is what this exercises.
func TestClientKeyCannotUseGoogleDriveBackupRoutes(t *testing.T) {
	t.Setenv("GOOGLE_DRIVE_BACKUP_CLIENT_ID", "CLIENT_ID_VALUE")
	t.Setenv("GOOGLE_DRIVE_BACKUP_CLIENT_SECRET", "CLIENT_SECRET_VALUE")

	c := newCredRouter(t, nil)
	routes := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/settings/backup/google/status", ""},
		{http.MethodGet, "/api/settings/backup/google/authorize", ""},
		{http.MethodPost, "/api/settings/backup/google/connect", `{"code":"x","redirectUri":"http://127.0.0.1/cb","state":"s"}`},
		{http.MethodDelete, "/api/settings/backup/google", ""},
		{http.MethodGet, "/api/settings/backup/google/files", ""},
		{http.MethodPost, "/api/settings/backup/google/restore", `{"fileId":"file-1"}`},
		{http.MethodPost, "/api/settings/backup/google", `{}`},
		{http.MethodPost, "/api/settings/backup/google/upload", `{}`},
	}
	for _, tc := range routes {
		rec := c.do(tc.method, tc.path, tc.body, func(req *http.Request) {
			engineKeyAuth(req)
			req.Header.Set("x-9r-password", "123456")
			req.Header.Set("x-9r-backup-passphrase", "correct horse battery")
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if leakedDriveSecret(rec.Body.String()) {
			t.Errorf("%s %s leaked OAuth client credentials", tc.method, tc.path)
		}
	}

	status := c.do(http.MethodGet, "/api/settings/backup/google/status", "", cliTokenAuth)
	if status.Code != http.StatusOK {
		t.Fatalf("CLI status = %d, want 200: %s", status.Code, status.Body.String())
	}
	if leakedDriveSecret(status.Body.String()) {
		t.Fatal("status leaked OAuth client credentials")
	}
}

func leakedDriveSecret(body string) bool {
	return strings.Contains(body, "CLIENT_ID_VALUE") || strings.Contains(body, "CLIENT_SECRET_VALUE")
}
