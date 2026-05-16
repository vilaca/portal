package admission

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateCreatesValidCerts(t *testing.T) {
	dir := t.TempDir()
	dns := []string{"portal-webhook.portal.svc", "portal-webhook.portal.svc.cluster.local"}

	certPath, keyPath, err := LoadOrGenerate(dir, dns)
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if certPath != filepath.Join(dir, "tls.crt") {
		t.Errorf("certPath=%q", certPath)
	}
	if keyPath != filepath.Join(dir, "tls.key") {
		t.Errorf("keyPath=%q", keyPath)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("expected CERTIFICATE PEM block, got %v", block)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	dnsSet := map[string]bool{}
	for _, n := range cert.DNSNames {
		dnsSet[n] = true
	}
	for _, want := range dns {
		if !dnsSet[want] {
			t.Errorf("certificate missing DNS name %q (have %v)", want, cert.DNSNames)
		}
	}
	// Server auth EKU must be present.
	found := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ServerAuth EKU")
	}

	// File mode 0600.
	st, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("cert mode = %o, want 600", perm)
	}

	// ca.crt also written and parses.
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt: %v", err)
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		t.Fatalf("ca PEM decode failed")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("ca parse: %v", err)
	}
	if !caCert.IsCA {
		t.Errorf("ca.crt is not flagged as CA")
	}
}

func TestLoadOrGenerateReusesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	c1, k1, err := LoadOrGenerate(dir, []string{"x.svc"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	st1, _ := os.Stat(c1)
	c2, k2, err := LoadOrGenerate(dir, []string{"x.svc"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if c1 != c2 || k1 != k2 {
		t.Errorf("paths must be stable across calls")
	}
	st2, _ := os.Stat(c2)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("expected cert not regenerated; mod time changed")
	}
}

func TestLoadOrGenerateRejectsEmptyDir(t *testing.T) {
	if _, _, err := LoadOrGenerate("", []string{"x"}); err == nil {
		t.Fatalf("expected error for empty certDir")
	}
}

// The former TestPatchWebhookConfigurationIsStub was retired alongside the
// PatchWebhookConfiguration stub. The production cert-injection path now lives
// in EnsureCerts (initcerts.go); see initcerts_test.go for coverage.
