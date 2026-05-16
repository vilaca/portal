package admission

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	testNS      = "portal-system"
	testSecret  = "portal-webhook-cert"
	testWebhook = "portal.io"
	testService = "portal"
)

// preexistingWebhookConfig is a fixture VWC the tests seed so EnsureCerts has
// a target to patch.
func preexistingWebhookConfig() *admissionv1.ValidatingWebhookConfiguration {
	sideEffects := admissionv1.SideEffectClassNone
	failPolicy := admissionv1.Fail
	return &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: testWebhook},
		Webhooks: []admissionv1.ValidatingWebhook{
			{
				Name:                    "validate.portal.io",
				SideEffects:             &sideEffects,
				FailurePolicy:           &failPolicy,
				AdmissionReviewVersions: []string{"v1"},
				ClientConfig:            admissionv1.WebhookClientConfig{},
			},
		},
	}
}

func TestEnsureCerts_FreshGeneratesEverything(t *testing.T) {
	tmp := t.TempDir()
	kube := fake.NewSimpleClientset(preexistingWebhookConfig())

	caPEM, err := EnsureCerts(context.Background(), kube, EnsureOptions{
		Namespace:     testNS,
		SecretName:    testSecret,
		WebhookConfig: testWebhook,
		Service:       testService,
		CertDir:       tmp,
	})
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	if len(caPEM) == 0 {
		t.Fatal("EnsureCerts returned empty CA bundle")
	}

	sec, err := kube.CoreV1().Secrets(testNS).Get(context.Background(), testSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	if sec.Type != corev1.SecretTypeTLS {
		t.Errorf("secret type = %s; want kubernetes.io/tls", sec.Type)
	}
	for _, k := range []string{"tls.crt", "tls.key", "ca.crt"} {
		if len(sec.Data[k]) == 0 {
			t.Errorf("secret missing %s", k)
		}
	}

	vwc, err := kube.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), testWebhook, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("VWC fetch: %v", err)
	}
	if string(vwc.Webhooks[0].ClientConfig.CABundle) != string(caPEM) {
		t.Errorf("CABundle not patched to the generated CA")
	}

	for _, name := range []string{"tls.crt", "tls.key", "ca.crt"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err != nil {
			t.Errorf("cert file %s not mirrored: %v", name, err)
		}
	}

	leaf, err := parseFirstCert(sec.Data["tls.crt"])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	for _, d := range []string{
		testService,
		testService + "." + testNS,
		testService + "." + testNS + ".svc",
		testService + "." + testNS + ".svc.cluster.local",
	} {
		found := false
		for _, got := range leaf.DNSNames {
			if got == d {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("leaf missing DNS SAN %q (have %v)", d, leaf.DNSNames)
		}
	}
}

func TestEnsureCerts_IdempotentWhenSecretValid(t *testing.T) {
	tmp := t.TempDir()
	kube := fake.NewSimpleClientset(preexistingWebhookConfig())

	ca1, err := EnsureCerts(context.Background(), kube, EnsureOptions{
		Namespace: testNS, SecretName: testSecret, WebhookConfig: testWebhook,
		Service: testService, CertDir: tmp,
	})
	if err != nil {
		t.Fatalf("first EnsureCerts: %v", err)
	}
	ca2, err := EnsureCerts(context.Background(), kube, EnsureOptions{
		Namespace: testNS, SecretName: testSecret, WebhookConfig: testWebhook,
		Service: testService, CertDir: tmp,
	})
	if err != nil {
		t.Fatalf("second EnsureCerts: %v", err)
	}
	if string(ca1) != string(ca2) {
		t.Error("CA bytes changed between idempotent runs — generation re-fired")
	}
}

func TestEnsureCerts_RegeneratesWhenCertNearExpiry(t *testing.T) {
	tmp := t.TempDir()
	kube := fake.NewSimpleClientset(preexistingWebhookConfig())

	// Seed a Secret with a leaf whose NotAfter is 24h away — inside the
	// default 30-day renew window.
	caPEM, leafPEM, leafKeyPEM := mintTestChain(t, []string{testService, testService + "." + testNS, testService + "." + testNS + ".svc"}, 24*time.Hour)
	if _, err := kube.CoreV1().Secrets(testNS).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSecret, Namespace: testNS},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": leafPEM,
			"tls.key": leafKeyPEM,
			"ca.crt":  caPEM,
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := EnsureCerts(context.Background(), kube, EnsureOptions{
		Namespace: testNS, SecretName: testSecret, WebhookConfig: testWebhook,
		Service: testService, CertDir: tmp,
	})
	if err != nil {
		t.Fatalf("EnsureCerts: %v", err)
	}
	if string(got) == string(caPEM) {
		t.Error("CA was not regenerated despite near-expiry seed")
	}
}

func TestEnsureCerts_RejectsMissingRequiredOption(t *testing.T) {
	kube := fake.NewSimpleClientset()
	if _, err := EnsureCerts(context.Background(), kube, EnsureOptions{Namespace: testNS}); err == nil {
		t.Fatal("expected error for missing required options")
	}
}

// mintTestChain produces a CA + leaf where the leaf has the requested DNS
// SANs and a NotAfter validFor from now. Self-contained — does not call
// generateSelfSigned because that helper hard-codes a 1-year validity.
func mintTestChain(t *testing.T, dnsNames []string, validFor time.Duration) (caPEM, leafPEM, leafKeyPEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "portal-test-ca"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(validFor + 1*time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca create: %v", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: "portal-test-leaf"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf create: %v", err)
	}
	leafPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	leafKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return caPEM, leafPEM, leafKeyPEM
}
