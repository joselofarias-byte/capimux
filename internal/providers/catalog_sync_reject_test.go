package providers

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestSyncModelCatalog_RejectsEmptyOrInvalidResponses pins the owner's rule
// (cycle 9, decision 2): an HTTP 200 whose body is null, {}, an empty catalog
// or not the models.dev shape keeps the previous catalog in memory, the cache
// file byte for byte and the stored ETag; the incident lands in LastError and
// the next sync can retry and succeed.
func TestSyncModelCatalog_RejectsEmptyOrInvalidResponses(t *testing.T) {
	good, err := os.ReadFile("testdata/modelsdev_variants_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"null", `null`},
		{"empty object", `{}`},
		{"empty body", ``},
		{"providers without models", `{"openai":{"models":{}},"anthropic":{}}`},
		{"array instead of object", `[]`},
		{"wrong models type", `{"openai":{"models":[]}}`},
		{"no limits nor modalities", `{"openai":{"models":{"gpt-x":{"name":"x"}}}}`},
		{"truncated JSON", `{"openai":{"models":{"gpt-x":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			if err := syncFromBody(t, good, path); err != nil {
				t.Fatalf("seed sync: %v", err)
			}
			syncStateMu.Lock()
			syncState.ETag = "etag-good"
			syncStateMu.Unlock()
			before := snapshotCatalog()
			fileBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			stateBefore := GetCatalogState()

			err = syncFromBodyKeepETag(t, []byte(tc.body), path)
			if err == nil {
				t.Fatal("expected the sync to be rejected")
			}
			if got := snapshotCatalog(); !reflect.DeepEqual(got, before) {
				t.Fatal("in-memory catalog replaced by a rejected response")
			}
			fileAfter, _ := os.ReadFile(path)
			if !bytes.Equal(fileAfter, fileBefore) {
				t.Fatal("cache file changed after a rejected response")
			}
			if ctx, _ := GetCatalogLimits("kilo", "poolside/laguna-s-2.1"); ctx != 1048576 {
				t.Fatalf("previous limits lost: %d", ctx)
			}
			st := GetCatalogState()
			if !strings.HasPrefix(st.LastError, "catálogo rechazado: ") {
				t.Fatalf("LastError = %q", st.LastError)
			}
			if st.ETag != "etag-good" || st.LastSync != stateBefore.LastSync || st.ModelCount != stateBefore.ModelCount || st.Running {
				t.Fatalf("sync state changed: %+v -> %+v", stateBefore, st)
			}
			matches, _ := filepath.Glob(path + ".tmp-*")
			if len(matches) != 0 {
				t.Fatalf("temp files left behind: %v", matches)
			}

			// A later good response is accepted: the failure does not block retries.
			if err := syncFromBody(t, good, path); err != nil {
				t.Fatalf("retry after a rejected response: %v", err)
			}
			if st := GetCatalogState(); st.LastError != "" {
				t.Fatalf("LastError not cleared after a good sync: %q", st.LastError)
			}
		})
	}
}

// syncFromBodyKeepETag is syncFromBody without clearing the stored ETag, so
// the test can check a rejected response leaves it alone.
func syncFromBodyKeepETag(t *testing.T, body []byte, filePath string) error {
	t.Helper()
	syncStateMu.Lock()
	etag := syncState.ETag
	syncStateMu.Unlock()
	return syncFromBodyWithETag(t, body, filePath, etag)
}
