package chat

import (
	"fmt"

	"9router/proxy/internal/constants"
	"9router/proxy/internal/log"
	"net/http"
	"net/url"
	"strings"
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
	strictProxy := connectionStrictProxy(connData)
	var relayPoolURLs []string

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
				if isEdgeRelayType(pool.Type) {
					relayPoolURLs = pool.URLs
				}
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
	// Under strictProxy the client is restricted to the relay hosts, so a
	// caller that did not rewrite its URL (for example after a provider-config
	// error) can never egress directly to the upstream.
	if isEdgeRelayType(proxyType) {
		if strictProxy {
			return h.strictRelayClient(connData.ProxyPoolID, relayPoolURLs)
		}
		return h.Client
	}
	// Unknown proxy types must never silently bypass a strict proxy policy.
	if strictProxy {
		return h.strictProxyFailClosed(connData.ProxyPoolID, "strict proxy uses unsupported proxy type %q", proxyType)
	}
	log.Warn("proxy", "unsupported proxy type; using direct client because strict proxy is disabled", "type", proxyType)
	return h.Client
}

// connectionStrictProxy reports whether the connection itself (top-level field
// or providerSpecificData.strictProxy) requires a proxy. The pool's own
// strictProxy flag is merged in by the callers that load the pool.
func connectionStrictProxy(connData *ConnectionData) bool {
	if connData == nil {
		return false
	}
	if connData.StrictProxy {
		return true
	}
	if connData.ProviderSpecificData != nil {
		if sp, ok := connData.ProviderSpecificData["strictProxy"].(bool); ok && sp {
			return true
		}
	}
	return false
}

// isEdgeRelayType reports whether a proxy pool type is an edge relay
// (requests are rewritten to the relay URL instead of dialing a proxy).
func isEdgeRelayType(proxyType string) bool {
	return proxyType == "vercel" || proxyType == "cloudflare" || proxyType == "deno"
}

// relayHostGuard only lets requests reach the configured edge relay hosts.
// Any other host would be direct egress, which a strict proxy policy forbids.
type relayHostGuard struct {
	base  http.RoundTripper
	hosts map[string]struct{}
}

func (g relayHostGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("strict proxy: request without URL")
	}
	if _, ok := g.hosts[strings.ToLower(req.URL.Host)]; !ok {
		return nil, fmt.Errorf("strict proxy: refusing direct request to %q outside the edge relay", req.URL.Host)
	}
	return g.base.RoundTrip(req)
}

func (h *ChatHandler) strictRelayClient(poolID string, relayURLs []string) *http.Client {
	hosts := make(map[string]struct{}, len(relayURLs))
	for _, raw := range relayURLs {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" {
			continue
		}
		hosts[strings.ToLower(u.Host)] = struct{}{}
	}
	if len(hosts) == 0 {
		return h.strictProxyFailClosed(poolID, "strict edge relay pool %q has no valid relay URL", poolID)
	}
	base := h.Client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport:     relayHostGuard{base: base, hosts: hosts},
		Timeout:       h.Client.Timeout,
		CheckRedirect: h.Client.CheckRedirect,
		Jar:           h.Client.Jar,
	}
}
