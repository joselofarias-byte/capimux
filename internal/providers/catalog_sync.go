package providers

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"9router/proxy/internal/log"
)

const (
	ModelsDevCatalogURL = "https://models.dev/api.json"
	CatalogSyncInterval = 24 * time.Hour
)

// SyncedModelModalities holds extracted capabilities from models.dev for a single model ID.
type SyncedModelModalities struct {
	Vision     bool `json:"vision"`
	PDF        bool `json:"pdf"`
	AudioInput bool `json:"audioInput"`
	VideoInput bool `json:"videoInput"`
}

// SyncedModelLimits holds token limits for a provider/model pair.
type SyncedModelLimits struct {
	ContextWindow int `json:"contextWindow,omitempty"`
	MaxOutput     int `json:"maxOutput,omitempty"`
}

// SyncedModelVariant is one upstream id that shares its catalog base with
// other ids of the same provider ("nvidia/nemotron-3-ultra" and
// "nvidia/nemotron-3-ultra:free") but declares different limits or
// modalities. The base key keeps the preferred variant; the others keep their
// own, unmixed values here, keyed by their full lowercased id.
type SyncedModelVariant struct {
	ContextWindow int                   `json:"contextWindow,omitempty"`
	MaxOutput     int                   `json:"maxOutput,omitempty"`
	Modalities    SyncedModelModalities `json:"modalities"`
}

// SyncedCatalog represents the processed catalog file written to disk / kept in
// memory. The file is shared with upstream, whose writer emits `syncedAt` as
// epoch milliseconds and adds `v`/`etag` fields — so SyncedAt is written as an
// ISO string and decoded leniently, and the modality map is keyed
// "<provider>:<model>" the way upstream keys it.
type SyncedCatalog struct {
	Version   int                                     `json:"v,omitempty"`
	ETag      string                                  `json:"etag,omitempty"`
	SyncedAt  string                                  `json:"syncedAt,omitempty"`
	Models    map[string]SyncedModelModalities        `json:"models"`
	Providers map[string]map[string]SyncedModelLimits `json:"providers"`
	// Variants is additive (absent in older files): provider -> full
	// lowercased upstream id -> that variant's own limits and modalities.
	Variants map[string]map[string]SyncedModelVariant `json:"variants,omitempty"`
}

// UnmarshalJSON accepts syncedAt as either an ISO string (this build's writer)
// or epoch milliseconds (upstream's writer), and tolerates it being absent —
// this build omits the field on the way out. Decoding it as []byte, as an
// earlier version did, makes encoding/json/v2 demand base64 and reject both
// real shapes, so LoadCatalogFromFile could never read a catalog back.
func (c *SyncedCatalog) UnmarshalJSON(data []byte) error {
	type alias SyncedCatalog
	aux := struct {
		SyncedAt any `json:"syncedAt"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	switch v := aux.SyncedAt.(type) {
	case nil:
		c.SyncedAt = ""
	case string:
		c.SyncedAt = v
	case float64:
		c.SyncedAt = time.UnixMilli(int64(v)).UTC().Format(time.RFC3339)
	default:
		return fmt.Errorf("catalog syncedAt: unexpected type %T", v)
	}
	return nil
}

type CatalogSyncState struct {
	Running    bool   `json:"running"`
	LastSync   string `json:"lastSync,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	ETag       string `json:"etag,omitempty"`
	ModelCount int    `json:"modelCount"`
}

var (
	catalogMu     sync.RWMutex
	globalCatalog *SyncedCatalog
	syncState     CatalogSyncState
	syncStateMu   sync.Mutex
)

// ProviderAliases maps 9router provider IDs to models.dev provider IDs for limit resolution.
var ProviderAliases = map[string]string{
	"glm":           "zai",
	"glm-cn":        "zhipuai",
	"claude":        "anthropic",
	"gemini":        "google",
	"kimi":          "moonshotai",
	"kimi-cn":       "moonshotai-cn",
	"qwen":          "alibaba",
	"qwen-cn":       "alibaba-cn",
	"zhipu":         "zhipuai",
	"hunyuan":       "tencent",
	"doubao":        "volcengine",
	"cloudflare-ai": "cloudflare-workers-ai",
}

// GetCatalogState returns the current synchronization state.
func GetCatalogState() CatalogSyncState {
	syncStateMu.Lock()
	defer syncStateMu.Unlock()
	return syncState
}

// catalogBaseID normalizes a model id the way both this sync and upstream's
// sync.js baseId() do: lowercase, then keep only the part after the last "/"
// and before the first ":" ("claude-opus-4-thinking:8192" -> "claude-opus-4-thinking").
func catalogBaseID(modelID string) string {
	base := strings.ToLower(modelID)
	if idx := strings.Index(base, "/"); idx != -1 {
		base = base[idx+1:]
	}
	if idx := strings.Index(base, ":"); idx != -1 {
		base = base[:idx]
	}
	return base
}

// catalogProviderKeys returns the ids a catalog entry may be filed under for a
// lookup on providerID: the provider itself, plus every local id that maps to
// it through ProviderAliases (our `claude` is models.dev `anthropic`). Mirrors
// sync.js localIds, so a lookup under either name resolves.
func catalogProviderKeys(providerID string) []string {
	keys := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		keys = append(keys, id)
	}
	add(providerID)
	for local, upstream := range ProviderAliases {
		if upstream == providerID {
			add(local)
		}
	}
	return keys
}

// catalogVariant returns the exact-id variant entry for provider/model, if the
// sync filed one. Variants are stored under the models.dev provider id, so
// the local alias is tried too.
func catalogVariant(provider, model string) (SyncedModelVariant, bool) {
	if globalCatalog == nil || globalCatalog.Variants == nil {
		return SyncedModelVariant{}, false
	}
	id := strings.ToLower(model)
	p := strings.ToLower(provider)
	keys := []string{p}
	if mapped, ok := ProviderAliases[p]; ok {
		keys = append(keys, mapped)
	}
	for _, key := range keys {
		if v, ok := globalCatalog.Variants[key][id]; ok {
			return v, true
		}
	}
	return SyncedModelVariant{}, false
}

// GetCatalogModalities looks up the synced input modalities for a
// provider/model pair. models.dev records modalities per gateway, and
// gateways disagree about the same weights (some do not proxy images at all),
// so the key is provider + model — matching the writer and upstream. A bare
// model key is still accepted so a catalog written by an older build keeps
// resolving.
func GetCatalogModalities(provider, model string) *SyncedModelModalities {
	if model == "" {
		return nil
	}
	base := catalogBaseID(model)

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if v, ok := catalogVariant(provider, model); ok {
		// The exact variant answers with its own modalities, even all-false:
		// it must not inherit another variant's.
		m := v.Modalities
		return &m
	}
	if globalCatalog == nil || globalCatalog.Models == nil {
		return nil
	}
	keys := catalogProviderKeys(strings.ToLower(provider))
	for _, key := range keys {
		if m, ok := globalCatalog.Models[key+":"+base]; ok {
			return &m
		}
	}
	if m, ok := globalCatalog.Models[base]; ok {
		return &m
	}
	return nil
}

// GetCatalogLimits looks up the models.dev-synced token limits for a
// provider/model pair. Provider ids are mapped through ProviderAliases first
// (our `claude` is models.dev `anthropic`, and so on), and the bare model id is
// accepted as a fallback key. This is the same per-provider+model source
// upstream getCapabilitiesForModel consults, so token limits stay in sync with
// the catalog instead of relying on substring heuristics.
func GetCatalogLimits(provider, model string) (contextWindow, maxOutput int) {
	if model == "" {
		return 0, 0
	}
	base := strings.ToLower(model)
	if idx := strings.Index(base, "/"); idx != -1 {
		base = base[idx+1:]
	}
	if idx := strings.Index(base, ":"); idx != -1 {
		base = base[:idx]
	}

	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if v, ok := catalogVariant(provider, model); ok {
		return v.ContextWindow, v.MaxOutput
	}
	if globalCatalog == nil || globalCatalog.Providers == nil {
		return 0, 0
	}
	keys := []string{strings.ToLower(provider)}
	if mapped, ok := ProviderAliases[strings.ToLower(provider)]; ok {
		keys = append(keys, mapped)
	}
	for _, key := range keys {
		byModel, ok := globalCatalog.Providers[key]
		if !ok {
			continue
		}
		if limits, ok := byModel[base]; ok {
			return limits.ContextWindow, limits.MaxOutput
		}
	}
	return 0, 0
}

// LoadCatalogFromFile loads cached catalog from disk if it exists.
func LoadCatalogFromFile(filePath string) error {
	if filePath == "" {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var cat SyncedCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return err
	}

	catalogMu.Lock()
	globalCatalog = &cat
	catalogMu.Unlock()

	syncStateMu.Lock()
	syncState.LastSync = cat.SyncedAt
	syncState.ModelCount = len(cat.Models)
	syncStateMu.Unlock()

	return nil
}

// SyncModelCatalog performs a download and parsing pass of models.dev API catalog.
func SyncModelCatalog(ctx context.Context, client *http.Client, filePath string) error {
	syncStateMu.Lock()
	if syncState.Running {
		syncStateMu.Unlock()
		return fmt.Errorf("sync already in progress")
	}
	syncState.Running = true
	syncStateMu.Unlock()

	defer func() {
		syncStateMu.Lock()
		syncState.Running = false
		syncStateMu.Unlock()
	}()

	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsDevCatalogURL, nil)
	if err != nil {
		return fmt.Errorf("create catalog request: %w", err)
	}

	syncStateMu.Lock()
	etag := syncState.ETag
	syncStateMu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := client.Do(req)
	if err != nil {
		syncStateMu.Lock()
		syncState.LastError = err.Error()
		syncStateMu.Unlock()
		return fmt.Errorf("catalog request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		log.Info("catalog_sync", "catalog not modified (304)")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		errText := fmt.Sprintf("catalog sync non-200 status: %d", resp.StatusCode)
		syncStateMu.Lock()
		syncState.LastError = errText
		syncStateMu.Unlock()
		return errors.New(errText)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50MB limit
	if err != nil {
		return rejectCatalog(resp.StatusCode, len(body), "no se pudo leer el cuerpo: "+err.Error())
	}

	// Validate first, publish after: an HTTP 200 whose body is null, {}, an
	// empty catalog or not the models.dev shape never replaces the catalog in
	// memory, the cache file or the stored ETag, so the next manual or
	// periodic sync retries from scratch.
	// models.dev format: map of providerID -> providerData { models: map[modelID]modelData }.
	var rawData map[string]modelsDevProvider
	if err := json.Unmarshal(body, &rawData); err != nil {
		return rejectCatalog(resp.StatusCode, len(body), "estructura inválida: "+err.Error())
	}
	if reason := invalidCatalogReason(rawData); reason != "" {
		return rejectCatalog(resp.StatusCode, len(body), reason)
	}

	modelsMap, providersMap, variantsMap := buildSyncedCatalog(rawData)
	if len(modelsMap) == 0 && len(providersMap) == 0 {
		return rejectCatalog(resp.StatusCode, len(body), "ningún modelo declara límites ni modalidades")
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	synced := &SyncedCatalog{
		SyncedAt:  nowStr,
		Models:    modelsMap,
		Providers: providersMap,
		Variants:  variantsMap,
	}

	// The cache file is replaced atomically (temp file + rename), so a crash
	// or a full disk leaves the previous file intact, never a truncated one.
	if filePath != "" {
		if err := writeCatalogFileAtomic(filePath, synced); err != nil {
			log.Warn("catalog_sync", "could not write the catalog cache; keeping the previous file", "error", err)
		}
	}

	catalogMu.Lock()
	globalCatalog = synced
	catalogMu.Unlock()
	InvalidateCapabilitiesCache()

	newEtag := resp.Header.Get("ETag")
	syncStateMu.Lock()
	syncState.LastSync = nowStr
	syncState.LastError = ""
	syncState.ETag = newEtag
	syncState.ModelCount = len(modelsMap)
	syncStateMu.Unlock()

	log.Info("catalog_sync", "catalog synchronized successfully", "models", len(modelsMap), "providers", len(providersMap))
	return nil
}

// invalidCatalogReason reports why a decoded feed must not replace the
// catalog, or "" when it is usable: null, {}, providers without models, or no
// model at all.
func invalidCatalogReason(rawData map[string]modelsDevProvider) string {
	if rawData == nil {
		return "respuesta vacía (null)"
	}
	if len(rawData) == 0 {
		return "catálogo vacío ({})"
	}
	total := 0
	for _, p := range rawData {
		total += len(p.Models)
	}
	if total == 0 {
		return "ningún proveedor trae modelos"
	}
	return ""
}

// rejectCatalog records a refused sync: LastError for the panel and a warning
// with only status, size and reason (never the body). The previous catalog,
// cache file and ETag stay as they were.
func rejectCatalog(status, size int, reason string) error {
	syncStateMu.Lock()
	syncState.LastError = "catálogo rechazado: " + reason
	syncStateMu.Unlock()
	log.Warn("catalog_sync", "catalog response rejected; keeping the previous catalog", "status", status, "bytes", size, "reason", reason)
	return fmt.Errorf("catalog rejected: %s", reason)
}

func writeCatalogFileAtomic(filePath string, cat *SyncedCatalog) error {
	outBytes, err := json.Marshal(cat, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(filePath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(outBytes); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, filePath)
}

// StartBackgroundCatalogSync runs initial sync and 24h timer loop.
func StartBackgroundCatalogSync(ctx context.Context, client *http.Client, filePath string) {
	_ = LoadCatalogFromFile(filePath)

	go func() {
		// Wait 30s after boot for first sync
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		if err := SyncModelCatalog(ctx, client, filePath); err != nil {
			log.Warn("catalog_sync", "initial sync failed", "error", err)
		}

		ticker := time.NewTicker(CatalogSyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := SyncModelCatalog(ctx, client, filePath); err != nil {
					log.Warn("catalog_sync", "periodic sync failed", "error", err)
				}
			}
		}
	}()
}
