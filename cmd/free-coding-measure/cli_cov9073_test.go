package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/measurement/freecoding"
)

func TestCLIRejectsIncompleteArgs_Cov9073(t *testing.T) {
	dir := t.TempDir()
	badJSONL := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(badJSONL, []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asDir := filepath.Join(dir, "out-is-dir")
	if err := os.Mkdir(asDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		args      []string
		code      int
		stderrHas string
		stderrNot string
		inference bool
	}{
		{name: "no args", code: 2, stderrHas: "free-coding-measure offline"},
		{name: "unknown command", args: []string{"nope"}, code: 2, stderrHas: "Usage:"},
		{name: "help", args: []string{"help"}, code: 0, stderrHas: "Tiers stay separate"},
		{name: "short help", args: []string{"-h"}, code: 0, stderrHas: "Default measurement is offline"},
		{name: "long help", args: []string{"--help"}, code: 0, stderrHas: "OPENROUTER_API_KEY is ignored"},
		{name: "offline missing out", args: []string{"offline"}, code: 2, stderrHas: "-out is required"},
		{name: "offline unknown flag", args: []string{"offline", "-out", dir, "-z"}, code: 2, stderrHas: "flag provided but not defined"},
		{name: "discover missing out", args: []string{"discover"}, code: 2, stderrHas: "-out is required", stderrNot: "discover:"},
		{name: "report missing jsonl", args: []string{"report"}, code: 2, stderrHas: "-jsonl is required"},
		{name: "report invalid jsonl", args: []string{"report", "-jsonl", badJSONL}, code: 1, stderrHas: "report:"},
		{name: "report out is a directory", args: []string{"report", "-jsonl", writeAllowCov9073(t, "cline/m"), "-out", asDir}, code: 1, stderrHas: "report:"},
		{name: "infer run below one", args: []string{"infer", "-out", dir, "-run", "0"}, code: 2, stderrHas: "-run must be >= 1"},
		{name: "infer without models", args: []string{"infer", "-out", dir, "-allow", writeAllowCov9073(t, "cline/m")}, code: 2, stderrHas: "one or more provider/model-id", inference: true},
		{name: "infer without allow file flag", args: []string{"infer", "-out", dir, "openrouter/alpha:free"}, code: 2, stderrHas: "needs -allow", inference: true},
		{name: "infer allow file missing", args: []string{"infer", "-out", dir, "-allow", filepath.Join(dir, "missing.jsonl"), "openrouter/alpha:free"}, code: 1, stderrHas: "allowlist:", inference: true},
		{name: "infer model spec", args: []string{"infer", "-out", dir, "-allow", writeAllowCov9073(t, "cline/m"), "noslash"}, code: 2, stderrHas: "want provider/model-id", inference: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := map[string]string{}
			if tt.inference {
				extra["FREE_CODING_INFERENCE"] = "1"
			}
			stdout, stderr, code := runCLICov9073(t, 20*time.Second, tt.args, extra)
			if code != tt.code {
				failCLICov9073(t, "", stdout, stderr, "exit %d, want %d", code, tt.code)
			}
			if !strings.Contains(stderr, tt.stderrHas) {
				failCLICov9073(t, "", stdout, stderr, "stderr missing %q", tt.stderrHas)
			}
			if tt.stderrNot != "" && strings.Contains(stderr, tt.stderrNot) {
				failCLICov9073(t, "", stdout, stderr, "stderr unexpectedly contains %q", tt.stderrNot)
			}
		})
	}
}

func TestOfflineThenReport_Cov9073(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runCLICov9073(t, 120*time.Second, []string{"offline", "-out", dir}, nil)
	if code != 0 {
		failCLICov9073(t, "", stdout, stderr, "offline exit %d", code)
	}
	reportPath := filepath.Join(dir, "report.md")
	jsonlPath := filepath.Join(dir, "runs.jsonl")
	if !strings.Contains(stdout, "REPORTE="+reportPath) {
		failCLICov9073(t, "", stdout, stderr, "missing report path")
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "# Free-coding measurement") {
		t.Fatal("offline report missing title")
	}
	recs, err := readJSONL(jsonlPath)
	if err != nil {
		t.Fatalf("offline jsonl: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("offline jsonl is empty")
	}

	outFile := filepath.Join(dir, "again.md")
	stdout, stderr, code = runCLICov9073(t, 20*time.Second, []string{"report", "-jsonl", jsonlPath, "-out", outFile}, nil)
	if code != 0 {
		failCLICov9073(t, "", stdout, stderr, "report exit %d", code)
	}
	again, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(report) {
		t.Fatal("report -out does not match the bundle report")
	}

	stdout, stderr, code = runCLICov9073(t, 20*time.Second, []string{"report", "-jsonl", jsonlPath}, nil)
	if code != 0 {
		failCLICov9073(t, "", stdout, stderr, "report stdout exit %d", code)
	}
	if stdout != string(report) {
		t.Fatal("report on stdout does not match the bundle report")
	}
}

func TestFinishDiscoverSummary_Cov9073(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)
	tools := true
	recs := []freecoding.Record{
		{
			RecordType: freecoding.RecordCatalog,
			UTC:        now,
			Tier:       freecoding.TierDiscovery,
			Provider:   "cline",
			HTTPStatus: 200,
			Provenance: "cov9073",
			QuotaScope: freecoding.QuotaNotApplicable,
		},
		{
			RecordType:            freecoding.RecordTrial,
			UTC:                   now,
			Tier:                  freecoding.TierDiscovery,
			Provider:              "cline",
			ModelID:               "eligible",
			PricingMode:           "free",
			ToolsDeclared:         &tools,
			CodingProfileEligible: true,
			Provenance:            "cov9073",
			QuotaScope:            freecoding.QuotaNotApplicable,
			Note:                  "local eligibility, not a ranking",
		},
		{
			RecordType:  freecoding.RecordTrial,
			UTC:         now,
			Tier:        freecoding.TierDiscovery,
			Provider:    "openrouter",
			ModelID:     "plain:free",
			PricingMode: "free_tier",
			Provenance:  "cov9073",
			QuotaScope:  freecoding.QuotaNotApplicable,
			Note:        "local eligibility, not a ranking",
		},
	}
	stdout, stderr := captureStdCov9073(t, func() { finish(dir, recs, true) })
	if !strings.Contains(stderr, "trial candidates=2 coding_profile_eligible=1") {
		t.Fatalf("stderr %q", stderr)
	}
	if !strings.Contains(stdout, "REPORTE="+filepath.Join(dir, "report.md")) {
		t.Fatalf("stdout %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestFinishExitsWhenCatalogsFail_Cov9073(t *testing.T) {
	if os.Getenv("FREE_CODING_FINISH_FAIL_COV9073") == "1" {
		dir := os.Getenv("FREE_CODING_OUT_COV9073")
		now := time.Now().UTC().Format(time.RFC3339)
		finish(dir, []freecoding.Record{{
			RecordType: freecoding.RecordCatalog,
			UTC:        now,
			Tier:       freecoding.TierDiscovery,
			Provider:   "openrouter",
			Skip:       true,
			SkipReason: "catalog unreachable",
			Provenance: "cov9073",
			QuotaScope: freecoding.QuotaNotApplicable,
		}}, true)
		return
	}

	dir := t.TempDir()
	stdout, stderr, code := runChildCov9073(t, 20*time.Second, "^TestFinishExitsWhenCatalogsFail_Cov9073$", map[string]string{
		"FREE_CODING_FINISH_FAIL_COV9073": "1",
		"FREE_CODING_OUT_COV9073":         dir,
	})
	if code != 1 {
		failCLICov9073(t, "", stdout, stderr, "exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "trial candidates=0 coding_profile_eligible=0") {
		failCLICov9073(t, "", stdout, stderr, "missing discover summary")
	}
	if !strings.Contains(stdout, "REPORTE=") {
		failCLICov9073(t, "", stdout, stderr, "missing report path")
	}
}

func TestFinishExitsWhenWriteFails_Cov9073(t *testing.T) {
	if os.Getenv("FREE_CODING_FINISH_WRITE_COV9073") == "1" {
		finish(os.Getenv("FREE_CODING_OUT_COV9073"), nil, false)
		return
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runChildCov9073(t, 20*time.Second, "^TestFinishExitsWhenWriteFails_Cov9073$", map[string]string{
		"FREE_CODING_FINISH_WRITE_COV9073": "1",
		"FREE_CODING_OUT_COV9073":          file,
	})
	if code != 1 || !strings.Contains(stderr, "write:") {
		failCLICov9073(t, "", stdout, stderr, "exit %d", code)
	}
}
