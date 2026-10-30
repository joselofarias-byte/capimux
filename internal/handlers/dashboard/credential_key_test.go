package dashboard

import "testing"

func TestChangedConnectionCredential(t *testing.T) {
	stored := `{"apiKey":"a","maxTokens":10,"providerSpecificData":{"refreshToken":"r","tokenType":"bearer","label":"x"}}`
	for _, tc := range []struct {
		name, data, want string
		changed          bool
	}{
		{"identical", stored, "", false},
		{"metadata only", `{"apiKey":"a","maxTokens":99,"providerSpecificData":{"refreshToken":"r","tokenType":"mac","label":"y"}}`, "", false},
		{"api key", `{"apiKey":"b","maxTokens":10,"providerSpecificData":{"refreshToken":"r"}}`, "apiKey", true},
		{"api key removed", `{"maxTokens":10,"providerSpecificData":{"refreshToken":"r"}}`, "apiKey", true},
		{"nested token", `{"apiKey":"a","providerSpecificData":{"refreshToken":"evil"}}`, "providerSpecificData.refreshToken", true},
		{"new cookie", `{"apiKey":"a","providerSpecificData":{"refreshToken":"r"},"sessionCookie":"c"}`, "sessionCookie", true},
		{"unparseable", `not json`, "data", true},
	} {
		got, changed := changedConnectionCredential(stored, tc.data)
		if changed != tc.changed || got != tc.want {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", tc.name, got, changed, tc.want, tc.changed)
		}
	}
}

func TestConnectionDestinationIn(t *testing.T) {
	for _, tc := range []struct {
		data, want string
		found      bool
	}{
		{``, "", false},
		{`{"apiKey":"k"}`, "", false},
		{`{"apiKey":"k","providerSpecificData":{"baseUrl":""}}`, "", false},
		{`{"baseUrl":"https://x.invalid"}`, "baseUrl", true},
		{`{"providerSpecificData":{"chatEndpoint":"https://x.invalid"}}`, "providerSpecificData.chatEndpoint", true},
		{`{"proxyPoolId":"p"}`, "proxyPoolId", true},
		{`garbage`, "data", true},
	} {
		got, found := connectionDestinationIn(tc.data)
		if found != tc.found || got != tc.want {
			t.Errorf("%q: got (%q,%v), want (%q,%v)", tc.data, got, found, tc.want, tc.found)
		}
	}
}
