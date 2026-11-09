package log

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdefghijklmnop":    "Authorization: Bearer ***",
		"key sk-proj-ABCDEFGHIJKLMNOPQRSTUV fin":    "key sk-p...STUV fin",
		"google AIzaSyA1234567890abcdefghijklmnopq": "google AIza...nopq",
		"sin secretos aquí":                         "sin secretos aquí",
		"sk-corto":                                  "sk-corto",
	}
	for in, want := range cases {
		if got := RedactString(in); got != want {
			t.Errorf("RedactString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutputRedactsConsoleLine(t *testing.T) {
	ClearConsoleLogs()
	Warn("prueba", "falló con Bearer tok_1234567890abcdef",
		"apiKey", "sk-live-0123456789abcdefXYZ",
		"err", errors.New("upstream rechazó sk-proj-ABCDEFGHIJKLMNOPQRSTUV"),
		"password", "hunter2hunter2",
		"model", "gpt-4o")
	logs := ConsoleLogs()
	if len(logs) == 0 {
		t.Fatal("sin líneas capturadas")
	}
	line := logs[len(logs)-1]
	for _, leak := range []string{"tok_1234567890abcdef", "sk-live-0123456789abcdefXYZ", "ABCDEFGHIJKLMNOPQRSTUV", "hunter2hunter2"} {
		if strings.Contains(line, leak) {
			t.Fatalf("secreto filtrado %q en %q", leak, line)
		}
	}
	if !strings.Contains(line, "model=gpt-4o") {
		t.Fatalf("valores no secretos deben quedar: %q", line)
	}
}

func TestOutputRedactsJSONMode(t *testing.T) {
	SetJSONFormat(true)
	defer SetJSONFormat(false)
	ClearConsoleLogs()
	Warn("prueba", "x", "authorization", "Bearer abcdefghijklmnop", "conn", "conn-123")
	logs := ConsoleLogs()
	line := logs[len(logs)-1]
	if strings.Contains(line, "abcdefghijklmnop") || !strings.Contains(line, "conn-123") {
		t.Fatalf("línea JSON = %q", line)
	}
}

// The caller's kv slice must not be mutated.
func TestOutputDoesNotMutateCallerArgs(t *testing.T) {
	kv := []any{"apiKey", "sk-live-0123456789abcdefXYZ"}
	Warn("prueba", "x", kv...)
	if kv[1] != "sk-live-0123456789abcdefXYZ" {
		t.Fatal("se mutó el slice del llamador")
	}
}
