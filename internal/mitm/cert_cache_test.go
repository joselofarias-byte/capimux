package mitm

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidCertDomain(t *testing.T) {
	long := strings.Repeat("a", 253)
	tests := []struct {
		domain string
		ok     bool
	}{
		{domain: "api.cursor.com", ok: true},
		{domain: "A1_b-c.example", ok: true},
		{domain: long, ok: true},
		{domain: "", ok: false},
		{domain: long + "a", ok: false},
		{domain: "evil.example/../../etc/passwd", ok: false},
		{domain: `..\windows\system32`, ok: false},
		{domain: "has space.com", ok: false},
		{domain: "unicode-é.example", ok: false},
	}
	for _, tt := range tests {
		if got := validCertDomain(tt.domain); got != tt.ok {
			t.Errorf("validCertDomain(%q) = %v, want %v", tt.domain, got, tt.ok)
		}
	}
}

func TestGetOrCreateLeafCert_RejectsPathEscape(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	dir := t.TempDir()
	ca, key, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	for _, domain := range []string{"", "../etc/passwd", "a/b", "has space", strings.Repeat("a", 254)} {
		if _, _, err := GetOrCreateLeafCert(dir, domain, ca, key); err == nil {
			t.Fatalf("domain %q was accepted", domain)
		}
	}
	outside := filepath.Join(filepath.Dir(dir), "passwd.pem")
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("leaf write escaped the temp dir: %s", outside)
	}
}

func TestGetOrCreateLeafCert_RegeneratesExpiredAndCorruptCache(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	dir := t.TempDir()
	ca, caKey, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	const domain = "expired.example"
	if err := writeExpiredLeaf(t, dir, domain, ca, caKey); err != nil {
		t.Fatalf("write expired leaf: %v", err)
	}

	fresh, _, err := GetOrCreateLeafCert(dir, domain, ca, caKey)
	if err != nil {
		t.Fatalf("GetOrCreateLeafCert: %v", err)
	}
	if !fresh.NotAfter.After(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("cached cert was not regenerated, NotAfter=%s", fresh.NotAfter)
	}
	if err := fresh.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("regenerated cert: %v", err)
	}

	certPath := filepath.Join(CertDir(dir), "corrupt.example.pem")
	keyPath := filepath.Join(CertDir(dir), "corrupt.example-key.pem")
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := GetOrCreateLeafCert(dir, "corrupt.example", ca, caKey)
	if err != nil {
		t.Fatalf("corrupt cache: %v", err)
	}
	if !replacement.NotAfter.After(time.Now()) {
		t.Fatal("corrupt cache was not replaced")
	}

	info, err := os.Stat(filepath.Join(CertDir(dir), "corrupt.example-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("leaf key is group or world accessible: %v", info.Mode())
	}
}

func TestGetOrCreateLeafCert_ConcurrentCallsShareOneCert(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	dir := t.TempDir()
	ca, key, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	const n = 8
	certs := make([]*x509.Certificate, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			certs[i], _, errs[i] = GetOrCreateLeafCert(dir, "race.example.com", ca, key)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if !certs[0].Equal(certs[i]) {
			t.Fatalf("goroutine %d minted a different cert", i)
		}
	}
}

func TestEnsureRootCA_ReplacesCorruptFilesWithoutInstalling(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	if !strings.HasSuffix(os.Args[0], ".test") {
		t.Fatal("refusing to call installRootCA")
	}
	dir := t.TempDir()
	certPath, keyPath := rootCAPaths(dir)
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, key, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	if cert == nil || !cert.IsCA || key == nil {
		t.Fatal("expected a replacement CA")
	}
	if err := installRootCA(certPath); err != nil {
		t.Fatalf("installRootCA: %v", err)
	}
}

func TestLoadRootCA_RejectsMissingAndMalformedPEM(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if _, _, err := loadRootCA(certPath, keyPath); err == nil {
		t.Fatal("missing files were accepted")
	}
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nbm90LWEgY2VydA==\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PRIVATE KEY-----\nbm90LWEta2V5\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadRootCA(certPath, keyPath); err == nil {
		t.Fatal("malformed PEM was accepted")
	}
}

func TestPrivateKeyToPEM_UnknownKeyType(t *testing.T) {
	if privateKeyToPEM(notAKey{}) != nil {
		t.Fatal("expected nil PEM for a key that cannot be marshaled")
	}
}

type notAKey struct{}

func (notAKey) Public() crypto.PublicKey { return nil }

func (notAKey) Equal(crypto.PrivateKey) bool { return false }

func writeExpiredLeaf(t *testing.T, dir, domain string, ca *x509.Certificate, caKey crypto.PrivateKey) error {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(CertDir(dir), 0o700); err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(CertDir(dir), domain+".pem"), certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(CertDir(dir), domain+"-key.pem"), keyPEM, 0o600)
}
