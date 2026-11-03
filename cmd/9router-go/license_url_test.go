package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withLicenseTestEnv isolates DATA_DIR, clears NINEROUTER_LICENSE_URL and pins
// a dev build identity so the tests never read the real ~/.9router or reach a
// real control plane.
func withLicenseTestEnv(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv(licenseURLEnv, "")
	oldChannel, oldID, oldUntil := licenseBuildChannel, licenseBuildID, licenseProCapableUntil
	t.Cleanup(func() {
		licenseBuildChannel, licenseBuildID, licenseProCapableUntil = oldChannel, oldID, oldUntil
	})
	licenseBuildChannel, licenseBuildID, licenseProCapableUntil = "dev", "", ""
	return dataDir
}

func TestLicenseURLHasNoBuiltInDefault(t *testing.T) {
	withLicenseTestEnv(t)
	got, err := currentLicenseControlPlaneURL()
	if !errors.Is(err, ErrLicenseURLNotConfigured) {
		t.Fatalf("missing URL error = %v, want ErrLicenseURLNotConfigured", err)
	}
	if got != "" {
		t.Fatalf("missing URL resolved to %q, want empty", got)
	}

	t.Setenv(licenseURLEnv, "   ")
	if _, err := currentLicenseControlPlaneURL(); !errors.Is(err, ErrLicenseURLNotConfigured) {
		t.Fatalf("blank URL error = %v, want ErrLicenseURLNotConfigured", err)
	}
}

// Static guard: the prototype Worker URL (or any workers.dev host) must never be
// compiled back into the client as a default.
func TestLicenseSourceHasNoPrototypeURL(t *testing.T) {
	raw, err := os.ReadFile("license.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, forbidden := range []string{"workers.dev", "licensing-prototype", "defaultLicenseControlPlaneURL"} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("license.go still contains %q", forbidden)
		}
	}
}

func TestLicenseNetworkCommandsWithoutURLFailBeforeTouchingState(t *testing.T) {
	cases := map[string]func(context.Context) error{
		"activate": func(ctx context.Context) error { return activateLicense(ctx, "INVITE-CODE-TEST") },
		"renew":    renewLicense,
		"release":  releaseLicense,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := withLicenseTestEnv(t)
			err := run(context.Background())
			if !errors.Is(err, ErrLicenseURLNotConfigured) {
				t.Fatalf("%s without URL = %v, want ErrLicenseURLNotConfigured", name, err)
			}
			if strings.Contains(err.Error(), "INVITE-CODE-TEST") {
				t.Fatalf("%s error leaks the activation code: %v", name, err)
			}
			if _, statErr := os.Stat(filepath.Join(dataDir, "license")); !os.IsNotExist(statErr) {
				t.Fatalf("%s without URL created license state (stat err = %v)", name, statErr)
			}
		})
	}
}

func TestLicenseStatusWorksOfflineWithoutURL(t *testing.T) {
	withLicenseTestEnv(t)
	if err := showLicenseStatus(); err != nil {
		t.Fatalf("status without URL and without license = %v, want Community without error", err)
	}
}
