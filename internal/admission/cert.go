package admission

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// LoadOrGenerate returns paths to a TLS leaf certificate and key inside
// certDir. If tls.crt and tls.key already exist in certDir, their paths are
// returned. Otherwise an in-memory self-signed CA + leaf are generated, the
// leaf is written to tls.crt / tls.key (0600), and the CA cert is written to
// ca.crt for later patching of the ValidatingWebhookConfiguration.caBundle.
//
// The leaf's SANs cover dnsNames (and 127.0.0.1 / localhost so tests can dial
// the server). dnsNames should match the in-cluster Service DNS — e.g.
// portal-webhook.portal.svc, portal-webhook.portal.svc.cluster.local.
func LoadOrGenerate(certDir string, dnsNames []string) (certPath, keyPath string, err error) {
	if certDir == "" {
		return "", "", errors.New("LoadOrGenerate: empty certDir")
	}
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", certDir, err)
	}
	certPath = filepath.Join(certDir, "tls.crt")
	keyPath = filepath.Join(certDir, "tls.key")

	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	caPEM, leafPEM, leafKeyPEM, err := generateSelfSigned(dnsNames)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, leafPEM, 0o600); err != nil {
		return "", "", fmt.Errorf("write tls.crt: %w", err)
	}
	if err := os.WriteFile(keyPath, leafKeyPEM, 0o600); err != nil {
		return "", "", fmt.Errorf("write tls.key: %w", err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca.crt"), caPEM, 0o600); err != nil {
		return "", "", fmt.Errorf("write ca.crt: %w", err)
	}
	return certPath, keyPath, nil
}

// generateSelfSigned builds a single-use CA and a leaf cert signed by it.
// Returns PEM-encoded ca-cert, leaf-cert (concat: leaf + ca for chain delivery),
// and leaf-key.
func generateSelfSigned(dnsNames []string) (caCertPEM, leafCertPEM, leafKeyPEM []byte, err error) {
	// CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ca key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "portal-admission-ca"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ca create: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, err
	}
	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// Leaf
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("leaf key: %w", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: "portal-admission"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     append([]string{"localhost"}, dnsNames...),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("leaf create: %w", err)
	}
	leafCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, nil, err
	}
	leafKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return caCertPEM, leafCertPEM, leafKeyPEM, nil
}

// PatchWebhookConfiguration is a stub. The real client-go-based caBundle patch
// is out of scope for the v1 process and is instead handled by:
//
//   - The Helm chart's `pre-install` Job, which generates certs, writes a
//     Secret and patches the ValidatingWebhookConfiguration's caBundle; OR
//   - cert-manager's `Certificate` + the standard
//     `cert-manager.io/inject-ca-from` annotation on the WebhookConfiguration.
//
// In dev, run `kubectl patch validatingwebhookconfiguration <name> \
// --type=json -p '[{"op":"replace","path":"/webhooks/0/clientConfig/caBundle","value":"<b64>"}]'`
// against the ca.crt produced by LoadOrGenerate.
//
// We deliberately keep this stub so cross-references from wire-up code give
// the operator a clear error message instead of attempting an unsafe patch.
func PatchWebhookConfiguration(_ any, _ string, _ []byte) error {
	return errors.New("admission.PatchWebhookConfiguration: not implemented; use the chart's pre-install hook, cert-manager, or `kubectl patch` against ca.crt produced by LoadOrGenerate (see comment for details)")
}

func mustSerial() *big.Int {
	maxSerial := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, maxSerial)
	if err != nil {
		// crypto/rand failing here is fatal; the cert is bootstrap-critical.
		panic(fmt.Sprintf("admission.mustSerial: %v", err))
	}
	return n
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
