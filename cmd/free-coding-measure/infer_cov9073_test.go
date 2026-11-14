package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/measurement/freecoding"
)

func TestInferSkipsBeforeAnyRequest_Cov9073(t *testing.T) {
	const token = "cov9073-token"
	tests := []struct {
		name      string
		models    []string
		allow     []string
		endpoint  string
		inference string
		keyFor    string
		reasons   []string
		noteHas   string
	}{
		{
			name:      "flag off",
			models:    []string{"openrouter/alpha:free", "cline/beta"},
			inference: "0",
			keyFor:    "openrouter",
			reasons:   []string{"FREE_CODING_INFERENCE is not 1"},
			noteHas:   "models=openrouter/alpha:free,cline/beta",
		},
		{
			name:      "no free credential",
			models:    []string{"openrouter/alpha:free"},
			inference: "1",
			reasons:   []string{"no free credential"},
		},
		{
			name:      "openrouter model without free suffix",
			models:    []string{"openrouter/alpha"},
			inference: "1",
			keyFor:    "openrouter",
			reasons:   []string{"without a :free suffix"},
		},
		{
			name:      "virtual profile name",
			models:    []string{"openrouter/free"},
			inference: "1",
			keyFor:    "openrouter",
			reasons:   []string{"virtual profile"},
		},
		{
			name:      "provider outside the harness",
			models:    []string{"nope/model"},
			inference: "1",
			keyFor:    "generic",
			reasons:   []string{`provider "nope" is outside this harness`},
		},
		{
			name:      "model id may contain slashes and still miss the allowlist",
			models:    []string{"openrouter/vendor/model:free"},
			allow:     []string{"openrouter/other:free"},
			inference: "1",
			keyFor:    "openrouter",
			reasons:   []string{"not on the free-catalog allowlist"},
		},
		{
			name:      "cline model missing from the allowlist",
			models:    []string{"cline/some-model"},
			inference: "1",
			keyFor:    "cline",
			reasons:   []string{"not on the free-catalog allowlist"},
		},
		{
			name:      "listed model with a non-provider endpoint",
			models:    []string{"openrouter/listed:free"},
			allow:     []string{"openrouter/listed:free"},
			endpoint:  "http://127.0.0.1:1/v1/chat/completions",
			inference: "1",
			keyFor:    "openrouter",
			reasons:   []string{"refuse endpoint"},
		},
		{
			name:      "several models each skip",
			models:    []string{"nope/model", "openrouter/missing:free"},
			inference: "1",
			keyFor:    "openrouter",
			reasons: []string{
				`provider "nope" is outside this harness`,
				"not on the free-catalog allowlist",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			allow := writeAllowCov9073(t, tt.allow...)
			args := []string{"infer", "-out", dir, "-allow", allow}
			if tt.endpoint != "" {
				args = append(args, "-endpoint", tt.endpoint)
			}
			args = append(args, tt.models...)
			extra := map[string]string{"FREE_CODING_INFERENCE": tt.inference}
			switch tt.keyFor {
			case "openrouter":
				extra["OPENROUTER_FREE_API_KEY"] = token
			case "cline":
				extra["CLINE_FREE_API_KEY"] = token
			case "generic":
				extra["FREE_CODING_API_KEY"] = token
			}
			stdout, stderr, code := runCLICov9073(t, 20*time.Second, args, extra)
			if code != 0 {
				failCLICov9073(t, token, stdout, stderr, "exit %d", code)
			}
			assertInferSkipCov9073(t, inferSkipCheckCov9073{
				token: token, dir: dir, stdout: stdout, stderr: stderr,
				reasons: tt.reasons, noteHas: tt.noteHas,
			})
		})
	}
}

type inferSkipCheckCov9073 struct {
	token   string
	dir     string
	stdout  string
	stderr  string
	reasons []string
	noteHas string
}

func assertInferSkipCov9073(t *testing.T, c inferSkipCheckCov9073) {
	t.Helper()
	recs, err := readJSONL(filepath.Join(c.dir, "runs.jsonl"))
	if err != nil {
		failCLICov9073(t, c.token, c.stdout, c.stderr, "jsonl: %v", err)
	}
	if len(recs) != len(c.reasons) {
		failCLICov9073(t, c.token, c.stdout, c.stderr, "got %d records, want %d", len(recs), len(c.reasons))
	}
	for i, rec := range recs {
		assertOneSkipCov9073(t, i, rec, c.reasons[i])
	}
	if c.noteHas != "" && !strings.Contains(recs[0].Note, c.noteHas) {
		t.Fatalf("note %q missing %q", recs[0].Note, c.noteHas)
	}
	report, err := os.ReadFile(filepath.Join(c.dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(c.dir, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoTokenCov9073(t, c.token, c.stdout, c.stderr, string(report), string(raw))
}

func assertOneSkipCov9073(t *testing.T, i int, rec freecoding.Record, reason string) {
	t.Helper()
	if rec.RecordType != freecoding.RecordInferSkip || !rec.Skip {
		t.Fatalf("record %d type=%s skip=%v", i, rec.RecordType, rec.Skip)
	}
	if !strings.Contains(rec.SkipReason, reason) {
		t.Fatalf("reason %q does not contain %q", rec.SkipReason, reason)
	}
	if rec.HTTPStatus != 0 || rec.Error != "" {
		t.Fatalf("record %d looks like a request was sent: http=%d err=%q", i, rec.HTTPStatus, rec.Error)
	}
}
