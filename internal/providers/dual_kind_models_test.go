package providers

import "testing"

// Upstream's gemini registry declares gemini-2.5-pro/flash/flash-lite twice:
// once as chat models and again as speech-to-text models. ProviderModelKinds
// holds one kind per id and is consulted only to drop non-chat models, so
// tagging those ids "stt" hid Gemini's flagship chat models from /v1/models,
// from the family combo recommender and from free-route discovery.
func TestDualDeclaredModelsStayChatModels(t *testing.T) {
	for provider, models := range ProviderModels {
		count := map[string]int{}
		for _, m := range models {
			count[m]++
		}
		for m, n := range count {
			if n < 2 {
				continue
			}
			if kind := GetProviderModelKind(provider, m); kind != "" && kind != "llm" {
				t.Errorf("%s/%s is declared %d times (chat and %s) but its kind %q removes it from every chat list", provider, m, n, kind, kind)
			}
		}
	}
}

func TestGeminiChatModelsAreNotSpeechOnly(t *testing.T) {
	for _, m := range []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"} {
		if kind := GetProviderModelKind("gemini", m); kind != "" {
			t.Errorf("gemini/%s kind = %q, want a chat model", m, kind)
		}
	}
	// Negative: the speech-only and image entries keep their kinds.
	for m, want := range map[string]string{
		"gemini-2.5-flash-preview-tts":                "tts",
		"gemini-2.5-flash-native-audio-preview-09-17": "stt",
		"gemini-2.0-flash":                            "stt",
		"gemini-2.5-flash-image":                      "image",
		"gemini-embedding-001":                        "embedding",
	} {
		if got := GetProviderModelKind("gemini", m); got != want {
			t.Errorf("gemini/%s kind = %q, want %q", m, got, want)
		}
	}
}
