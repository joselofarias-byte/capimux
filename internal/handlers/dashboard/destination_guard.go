package dashboard

import (
	"encoding/json"
	"reflect"
	"strings"
)

// connectionDestinationKeys are connection data fields (top level or inside
// providerSpecificData) that decide where a stored credential is sent or which
// proxy carries it. Changing any of them on an existing connection lets the
// caller receive the owner's upstream API key or OAuth token on the next
// request, so a client (inference) API key must not be able to edit them.
var connectionDestinationKeys = map[string]bool{
	"baseUrl":                true,
	"connectionProxyUrl":     true,
	"connectionProxyEnabled": true,
	"connectionNoProxy":      true,
	"strictProxy":            true,
	"vercelRelayUrl":         true,
	"proxyPoolId":            true,
}

// isDestinationKey also treats any other "...Url"/"...Endpoint" field as a
// destination, so a future provider-specific URL is covered by default.
func isDestinationKey(key string) bool {
	if connectionDestinationKeys[key] {
		return true
	}
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "url") || strings.HasSuffix(lower, "endpoint")
}

// changedConnectionDestination compares the stored connection data with the
// data about to be written and returns the first destination field that
// differs. Unparseable new data that differs from the stored data is reported
// as a change, since it would replace the routing fields wholesale.
func changedConnectionDestination(oldData, newData string) (string, bool) {
	if oldData == newData {
		return "", false
	}
	oldMap := map[string]any{}
	if strings.TrimSpace(oldData) != "" {
		_ = json.Unmarshal([]byte(oldData), &oldMap)
	}
	newMap := map[string]any{}
	if strings.TrimSpace(newData) != "" {
		if err := json.Unmarshal([]byte(newData), &newMap); err != nil {
			return "data", true
		}
	}
	if key, changed := diffDestinationKeys(oldMap, newMap); changed {
		return key, true
	}
	oldPSD, _ := oldMap["providerSpecificData"].(map[string]any)
	newPSD, _ := newMap["providerSpecificData"].(map[string]any)
	if key, changed := diffDestinationKeys(oldPSD, newPSD); changed {
		return "providerSpecificData." + key, true
	}
	return "", false
}

func diffDestinationKeys(oldMap, newMap map[string]any) (string, bool) {
	seen := map[string]bool{}
	for _, m := range []map[string]any{oldMap, newMap} {
		for key := range m {
			if seen[key] || !isDestinationKey(key) {
				continue
			}
			seen[key] = true
			if !reflect.DeepEqual(oldMap[key], newMap[key]) {
				return key, true
			}
		}
	}
	return "", false
}

// proxyPoolDestinationKeys are proxy pool fields that decide which proxy
// carries the traffic of every connection bound to the pool.
var proxyPoolDestinationKeys = []string{"urls", "proxyUrl", "type", "noProxy", "strictProxy"}

// changedProxyPoolDestination reports the first routing field in updates whose
// value differs from the stored pool (compared through JSON so []string from
// the repo and []any from the request body match).
func changedProxyPoolDestination(current map[string]any, updates map[string]any) (string, bool) {
	for key, next := range updates {
		isRouting := isDestinationKey(key)
		for _, k := range proxyPoolDestinationKeys {
			if k == key {
				isRouting = true
			}
		}
		if !isRouting {
			continue
		}
		a, _ := json.Marshal(current[key])
		b, _ := json.Marshal(next)
		if string(a) != string(b) {
			return key, true
		}
	}
	return "", false
}

// isCredentialKey reports connection data fields that hold the secret itself
// (API key, OAuth tokens, cookies, client secrets). Rewriting one replaces the
// account behind an existing connection.
func isCredentialKey(key string) bool {
	lower := strings.ToLower(key)
	switch lower {
	case "apikey", "api_key", "key", "password", "cookie", "cookies":
		return true
	}
	// Metadata that merely mentions tokens (maxTokens, tokenType,
	// tokenExpiresAt, usage counters) is not a credential.
	for _, part := range []string{"max", "limit", "expir", "type", "usage", "count"} {
		if strings.Contains(lower, part) {
			return false
		}
	}
	for _, part := range []string{"token", "secret", "password", "cookie", "apikey", "api_key", "credential"} {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// changedConnectionCredential returns the first credential field (top level or
// inside providerSpecificData) whose stored value would change. Unparseable new
// data that differs from the stored data is reported as a change.
func changedConnectionCredential(oldData, newData string) (string, bool) {
	if oldData == newData {
		return "", false
	}
	oldMap := map[string]any{}
	if strings.TrimSpace(oldData) != "" {
		_ = json.Unmarshal([]byte(oldData), &oldMap)
	}
	newMap := map[string]any{}
	if strings.TrimSpace(newData) != "" {
		if err := json.Unmarshal([]byte(newData), &newMap); err != nil {
			return "data", true
		}
	}
	if key, changed := diffKeys(oldMap, newMap, isCredentialKey); changed {
		return key, true
	}
	oldPSD, _ := oldMap["providerSpecificData"].(map[string]any)
	newPSD, _ := newMap["providerSpecificData"].(map[string]any)
	if key, changed := diffKeys(oldPSD, newPSD, isCredentialKey); changed {
		return "providerSpecificData." + key, true
	}
	return "", false
}

// connectionDestinationIn returns the first non-empty destination field in the
// data of a connection about to be created. Unparseable data is reported as
// "data", since its routing fields cannot be checked.
func connectionDestinationIn(data string) (string, bool) {
	if strings.TrimSpace(data) == "" {
		return "", false
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return "data", true
	}
	if key, changed := diffKeys(nil, nonEmpty(m), isDestinationKey); changed {
		return key, true
	}
	psd, _ := m["providerSpecificData"].(map[string]any)
	if key, changed := diffKeys(nil, nonEmpty(psd), isDestinationKey); changed {
		return "providerSpecificData." + key, true
	}
	return "", false
}

func diffKeys(oldMap, newMap map[string]any, match func(string) bool) (string, bool) {
	seen := map[string]bool{}
	for _, m := range []map[string]any{oldMap, newMap} {
		for key := range m {
			if seen[key] || !match(key) {
				continue
			}
			seen[key] = true
			if !reflect.DeepEqual(oldMap[key], newMap[key]) {
				return key, true
			}
		}
	}
	return "", false
}

// nonEmpty drops nil and "" values, which select no destination.
func nonEmpty(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if v == nil || v == "" {
			continue
		}
		out[k] = v
	}
	return out
}
