package dashboard

import "testing"

// The family recommender must offer Gemini's chat models as fallbacks even
// though upstream also lists them as speech-to-text models.
func TestFamilyRecommenderKeepsGeminiDualKindModels(t *testing.T) {
	for _, m := range []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"} {
		if !isUsableChatModel("gemini", m, nil) {
			t.Errorf("gemini/%s excluded from chat fallback suggestions", m)
		}
	}
	if isUsableChatModel("gemini", "gemini-2.5-flash-preview-tts", nil) {
		t.Error("a TTS-only model was suggested as a chat fallback")
	}
	if isUsableChatModel("gemini", "gemini-2.5-pro", map[string]struct{}{"gemini-2.5-pro": {}}) {
		t.Error("a disabled model was suggested")
	}
}
