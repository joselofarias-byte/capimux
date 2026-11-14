package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/measurement/freecoding"
)

// TestMain re-executes this test binary as the CLI when
// FREE_CODING_CLI_ARGS_COV9073 is set. Exit paths call os.Exit, so they have
// to run in a child process. The child inherits GOCOVERDIR from go test.
func TestMain(m *testing.M) {
	raw := os.Getenv("FREE_CODING_CLI_ARGS_COV9073")
	if raw == "" {
		os.Exit(m.Run())
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		fmt.Fprintln(os.Stderr, "cli harness: args")
		os.Exit(2)
	}
	os.Args = append([]string{"free-coding-measure"}, args...)
	main()
	os.Exit(0)
}

func writeAllowCov9073(t *testing.T, specs ...string) string {
	t.Helper()
	if len(specs) == 0 {
		specs = []string{"other/not-used"}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var buf bytes.Buffer
	for _, spec := range specs {
		provider, model, err := splitModel(spec)
		if err != nil {
			t.Fatalf("allow spec: %v", err)
		}
		rec := freecoding.Record{
			RecordType:  freecoding.RecordTrial,
			UTC:         now,
			Tier:        freecoding.TierOffline,
			Provider:    provider,
			ModelID:     model,
			PricingMode: "free",
			Provenance:  "cov9073 local allowlist",
			QuotaScope:  freecoding.QuotaNotApplicable,
			Note:        "local allowlist row, not a ranking",
		}
		if err := json.NewEncoder(&buf).Encode(rec); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "allow.jsonl")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLICov9073(t *testing.T, timeout time.Duration, args []string, extra map[string]string) (string, string, int) {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if extra == nil {
		extra = map[string]string{}
	}
	extra["FREE_CODING_CLI_ARGS_COV9073"] = string(payload)
	return runChildCov9073(t, timeout, "^$", extra)
}

func runChildCov9073(t *testing.T, timeout time.Duration, run string, extra map[string]string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+run)
	cmd.Env = envCov9073(extra)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command timed out after %s", timeout)
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func envCov9073(extra map[string]string) []string {
	drop := map[string]struct{}{
		"FREE_CODING_CLI_ARGS_COV9073":     {},
		"FREE_CODING_FINISH_FAIL_COV9073":  {},
		"FREE_CODING_FINISH_WRITE_COV9073": {},
		"FREE_CODING_OUT_COV9073":          {},
		"FREE_CODING_INFERENCE":            {},
		"OPENROUTER_FREE_API_KEY":          {},
		"CLINE_FREE_API_KEY":               {},
		"FREE_CODING_API_KEY":              {},
		"OPENROUTER_API_KEY":               {},
	}
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := drop[key]; ok {
			continue
		}
		env = append(env, kv)
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func assertNoTokenCov9073(t *testing.T, token string, blobs ...string) {
	t.Helper()
	if token == "" {
		t.Fatal("empty token")
	}
	for i, blob := range blobs {
		if strings.Contains(blob, token) {
			t.Fatalf("output %d contains the test credential", i)
		}
	}
}

func failCLICov9073(t *testing.T, secret, stdout, stderr, format string, args ...any) {
	t.Helper()
	t.Fatalf("%s\nstdout:\n%s\nstderr:\n%s", fmt.Sprintf(format, args...), clipCov9073(redactCov9073(stdout, secret)), clipCov9073(redactCov9073(stderr, secret)))
}

func redactCov9073(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

func clipCov9073(s string) string {
	if len(s) > 800 {
		return s[:800] + "…"
	}
	return s
}

func captureStdCov9073(t *testing.T, fn func()) (string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	fn()
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var stdout, stderr bytes.Buffer
	if _, err := stdout.ReadFrom(outR); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.ReadFrom(errR); err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String()
}
