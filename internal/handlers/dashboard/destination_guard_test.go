package dashboard

import "testing"

func TestChangedConnectionDestination(t *testing.T) {
	base := `{"apiKey":"k","providerSpecificData":{"baseUrl":"https://a.example/v1","region":"us"}}`
	for _, tc := range []struct {
		name, next string
		want       bool
	}{
		{"identical", base, false},
		{"non destination change", `{"apiKey":"k","assignedModel":"m","providerSpecificData":{"baseUrl":"https://a.example/v1","region":"eu"}}`, false},
		{"psd baseUrl", `{"apiKey":"k","providerSpecificData":{"baseUrl":"https://b.example/v1"}}`, true},
		{"psd baseUrl removed", `{"apiKey":"k","providerSpecificData":{}}`, true},
		{"top-level proxyPoolId", `{"apiKey":"k","proxyPoolId":"p","providerSpecificData":{"baseUrl":"https://a.example/v1"}}`, true},
		{"future url field", `{"apiKey":"k","providerSpecificData":{"baseUrl":"https://a.example/v1","tokenEndpoint":"https://x"}}`, true},
		{"proxy toggle", `{"apiKey":"k","providerSpecificData":{"baseUrl":"https://a.example/v1","connectionProxyEnabled":true}}`, true},
		{"unparseable", `not-json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := changedConnectionDestination(base, tc.next); got != tc.want {
				t.Fatalf("changed=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestChangedProxyPoolDestination(t *testing.T) {
	current := map[string]any{"name": "p", "urls": []any{"http://a:1"}, "type": "http"}
	if _, changed := changedProxyPoolDestination(current, map[string]any{"name": "q", "urls": []any{"http://a:1"}, "type": "http"}); changed {
		t.Fatal("rename with unchanged urls must be allowed")
	}
	for _, upd := range []map[string]any{
		{"urls": []any{"http://b:1"}},
		{"proxyUrl": "http://b:1"},
		{"type": "vercel"},
		{"noProxy": "*"},
		{"strictProxy": true},
	} {
		if _, changed := changedProxyPoolDestination(current, upd); !changed {
			t.Fatalf("expected change for %#v", upd)
		}
	}
}
