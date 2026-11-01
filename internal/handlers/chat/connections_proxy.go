package chat

import (
	"fmt"

	"9router/proxy/internal/constants"
	"9router/proxy/internal/log"
	"net/http"
	"net/url"
	"sync"
)

// maxProxyClients caps rotating-proxy growth: each entry pins a Transport
// plus idle sockets, so unbounded distinct URLs (residential rotation)
// would leak descriptors without eviction.
const maxProxyClients = 128

var (
	proxyClientsMu sync.RWMutex
	proxyClients   = make(map[string]*http.Client)
	// proxyPoolLogged tracks pool IDs already announced at debug level so the
	// "routing via proxy" line appears once per pool, not on every request.
	proxyPoolLogged sync.Map
)

// logProxyOnce records proxy usage once per pool at debug level, e.g.
// proxy routing requests via proxy pool pool=abc123 url=http://... type=http
// Run with LOG_LEVEL=debug and grep for "proxy" to confirm traffic uses the pool.
func logProxyOnce(poolID, proxyURLStr, proxyType string) {
	if poolID == "" {
		return
	}
	if _, loaded := proxyPoolLogged.LoadOrStore(poolID, true); loaded {
		return
	}
	if proxyType == "" {
		proxyType = "http"
	}
	log.Debug("proxy", "routing requests via proxy pool", "pool", poolID, "url", proxyURLStr, "type", proxyType)
}

// GetClientForConnection returns an http.Client configured with ProxyPool transport if set.
func (h *ChatHandler) GetClientForConnection(connData *ConnectionData) *http.Client {
	return h.getClientForConnection(connData)
}

// failingRoundTripper is returned when a strict proxy policy cannot be honoured:
// every request fails instead of silently egressing directly.
type failingRoundTripper struct {
	err error
}

func (t failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func (h *ChatHandler) strictProxyFailClosed(poolID, format string, args ...any) *http.Client {
	proxyErr := fmt.Errorf(format, args...)
	log.Error("proxy", "strict proxy configuration unavailable; failing closed", "pool", poolID, "error", proxyErr)
	return &http.Client{Transport: failingRoundTripper{err: proxyErr}, Timeout: h.Client.Timeout}
}

func (h *ChatHandler) getClientForConnection(connData *ConnectionData) *http.Client {
	if connData == nil {
		return h.Client
	}

	var proxyURLStr string
	var proxyType string
	// Strictness comes from the connection, its provider-specific data, or the
	// pool. Any of them set means the request must never go out directly.
	strictProxy := connData.StrictProxy
	if connData.ProviderSpecificData != nil {
		if sp, ok := connData.ProviderSpecificData["strictProxy"].(bool); ok && sp {
			strictProxy = true
		}
	}

	// 1. Resolve from ProxyPool. Read strictness even for inactive pools so an
	// explicit strict policy cannot silently turn into direct egress.
	if connData.ProxyPoolID != "" {
		pool, err := h.Repo.GetProxyPool(connData.ProxyPoolID)
		if err != nil || pool == nil {
			if strictProxy {
				return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy pool %q is unavailable", connData.ProxyPoolID)
			}
		} else {
			strictProxy = strictProxy || pool.StrictProxy
			if !pool.IsActive {
				if strictProxy {
					return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy pool %q is inactive", connData.ProxyPoolID)
				}
			} else {
				proxyURLStr = pool.NextURL()
				proxyType = pool.Type
				if proxyURLStr == "" && strictProxy {
					return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy pool %q has no usable URL", connData.ProxyPoolID)
				}
			}
		}
	}

	// 2. Fallback to legacy connection proxy
	if proxyURLStr == "" {
		proxyEnabled := connData.ConnectionProxyEnabled
		proxyURL := connData.ConnectionProxyURL
		if connData.ProviderSpecificData != nil {
			if !proxyEnabled {
				if en, ok := connData.ProviderSpecificData["connectionProxyEnabled"].(bool); ok {
					proxyEnabled = en
				}
			}
			if proxyURL == "" {
				if u, ok := connData.ProviderSpecificData["connectionProxyUrl"].(string); ok {
					proxyURL = u
				}
			}
		}
		if proxyEnabled && proxyURL != "" {
			proxyURLStr = proxyURL
			proxyType = "http"
		}
	}

	if proxyURLStr == "" {
		if strictProxy {
			return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy is enabled but no usable proxy route is configured")
		}
		return h.Client
	}
	logProxyOnce(connData.ProxyPoolID, proxyURLStr, proxyType)

	parsedURL, err := url.Parse(proxyURLStr)
	if err != nil {
		log.Warn("proxy", "invalid proxy pool url", "pool", connData.ProxyPoolID, "error", err)
		if strictProxy {
			return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy URL is invalid: %v", err)
		}
		return h.Client
	}
	if proxyType == "http" || proxyType == "" {
		proxyClientsMu.RLock()
		client, ok := proxyClients[proxyURLStr]
		proxyClientsMu.RUnlock()
		if ok {
			return client
		}

		proxyClientsMu.Lock()
		defer proxyClientsMu.Unlock()
		if client, ok = proxyClients[proxyURLStr]; ok {
			return client
		}
		// Evict idle sockets of a random victim when over cap (amortized O(1);
		// exact LRU is overkill — URLs are hot or dead, never warm).
		if len(proxyClients) >= maxProxyClients {
			for victimURL, victim := range proxyClients {
				victim.CloseIdleConnections()
				delete(proxyClients, victimURL)
				break
			}
		}

		var baseTransport *http.Transport
		if origT, ok := http.DefaultTransport.(*http.Transport); ok {
			baseTransport = origT.Clone()
		} else {
			baseTransport = constants.DefaultHTTPTransportConfig.NewTransport()
		}
		constants.DefaultHTTPTransportConfig.Configure(baseTransport)
		baseTransport.Proxy = http.ProxyURL(parsedURL)
		client = &http.Client{
			Transport: baseTransport,
			Timeout:   h.Client.Timeout,
		}
		proxyClients[proxyURLStr] = client
		return client
	}

	// For supported Edge Relays, the standard client is intentional because URL
	// rewriting and x-relay headers are handled at request time.
	if proxyType == "vercel" || proxyType == "cloudflare" || proxyType == "deno" {
		return h.Client
	}
	// Unknown proxy types must never silently bypass a strict proxy policy.
	if strictProxy {
		return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy uses unsupported proxy type %q", proxyType)
	}
	log.Warn("proxy", "unsupported proxy type; using direct client because strict proxy is disabled", "type", proxyType)
	return h.Client
}
