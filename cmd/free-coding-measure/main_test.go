package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitModel_Cov9073(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		wantProvider string
		wantModel    string
		wantErr      bool
	}{
		{name: "provider and model", spec: "openrouter/alpha:free", wantProvider: "openrouter", wantModel: "alpha:free"},
		{name: "model id keeps extra slashes", spec: "cline/vendor/model", wantProvider: "cline", wantModel: "vendor/model"},
		{name: "missing slash", spec: "openrouter", wantErr: true},
		{name: "empty provider", spec: "/model", wantErr: true},
		{name: "empty model", spec: "openrouter/", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model, err := splitModel(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitModel() error = %v, wantErr %v", err, tt.wantErr)
			}
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("splitModel() = %q %q, want %q %q", provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

func TestCredentialSources_Cov9073(t *testing.T) {
	const (
		openrouter = "cov9073-openrouter"
		cline      = "cov9073-cline"
		generic    = "cov9073-generic"
		ambient    = "cov9073-ambient"
	)
	tests := []struct {
		name       string
		provider   string
		openrouter string
		cline      string
		generic    string
		ambient    string
		want       string
	}{
		{name: "openrouter prefers its free key", provider: "openrouter", openrouter: openrouter, generic: generic, ambient: ambient, want: openrouter},
		{name: "cline prefers its free key", provider: "cline", cline: cline, generic: generic, ambient: ambient, want: cline},
		{name: "openrouter falls back to the generic free key", provider: "openrouter", generic: generic, ambient: ambient, want: generic},
		{name: "cline falls back to the generic free key", provider: "cline", generic: generic, want: generic},
		{name: "other providers use only the generic free key", provider: "other", openrouter: openrouter, cline: cline, generic: generic, want: generic},
		{name: "ambient openrouter key is ignored", provider: "openrouter", ambient: ambient, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENROUTER_FREE_API_KEY", tt.openrouter)
			t.Setenv("CLINE_FREE_API_KEY", tt.cline)
			t.Setenv("FREE_CODING_API_KEY", tt.generic)
			t.Setenv("OPENROUTER_API_KEY", tt.ambient)
			if got := credential(tt.provider); got != tt.want {
				t.Fatalf("credential(%q) mismatch", tt.provider)
			}
		})
	}
}

func TestReadJSONL_Cov9073(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := readJSONL(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("blank lines around a trial", func(t *testing.T) {
		path := writeAllowCov9073(t, "openrouter/alpha:free")
		recs, err := readJSONL(path)
		if err != nil {
			t.Fatalf("readJSONL: %v", err)
		}
		if len(recs) != 1 || recs[0].ModelID != "alpha:free" {
			t.Fatalf("got %#v", recs)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.jsonl")
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := readJSONL(path)
		if err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestUsageText_Cov9073(t *testing.T) {
	_, stderr := captureStdCov9073(t, usage)
	for _, phrase := range []string{
		"free-coding-measure offline",
		"free-coding-measure discover",
		"free-coding-measure infer",
		"free-coding-measure report",
		"OPENROUTER_API_KEY is ignored",
		"no network",
	} {
		if !strings.Contains(stderr, phrase) {
			t.Errorf("usage missing %q", phrase)
		}
	}
}
