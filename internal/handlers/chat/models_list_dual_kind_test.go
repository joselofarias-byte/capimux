package chat

import "testing"

// gemini-2.5-pro is both a chat and a speech-to-text model upstream; the
// /v1/models LLM list must keep it.
func TestGeminiDualKindModelsStayInLLMList(t *testing.T) {
	for _, m := range []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"} {
		if !isLLMModelEntry("gemini", "gemini", "gemini", m, false) {
			t.Errorf("gemini/%s dropped from the LLM list", m)
		}
	}
	if isLLMModelEntry("gemini", "gemini", "gemini", "gemini-2.5-pro-preview-tts", false) {
		t.Error("a TTS-only model leaked into the LLM list")
	}
}
