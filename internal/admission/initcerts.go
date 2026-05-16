package admission

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// EnsureOptions configures one EnsureCerts run.
type EnsureOptions struct {
	// Namespace is where the TLS Secret lives.
	Namespace string
	// SecretName is the name of the kubernetes.io/tls Secret holding the
	// generated cert material (data: tls.crt, tls.key, ca.crt). The Secret
	// is created when absent and updated in-place otherwise.
	SecretName string
	// WebhookConfig is the ValidatingWebhookConfiguration to inject caBundle
	// into. Every webhook entry's clientConfig.caBundle is set to the CA.
	WebhookConfig string
	// Service is the in-cluster Service the webhook is reached through. DNS
	// SANs are derived as: <svc>, <svc>.<ns>, <svc>.<ns>.svc,
	// <svc>.<ns>.svc.cluster.local plus ExtraDNSNames.
	Service string
	// CertDir is a local path the orchestrator mirrors the final cert
	// material to (tls.crt, tls.key, ca.crt) so the main container's
	// filesystem mount has the files regardless of kubelet Secret refresh
	// timing. Typically a shared emptyDir mounted into both init and main.
	CertDir string
	// ExtraDNSNames are additional SANs beyond the service derivation.
	ExtraDNSNames []string
	// RenewBefore controls regeneration when the current cert's NotAfter is
	// within this window. Default 30 days.
	RenewBefore time.Duration
}

// DefaultRenewBefore is the conservative regeneration window. CAs and leaves
// are minted with 1-year validity; regenerating 30 days early gives operators
// a month of cushion before kubelet sees an expired cert.
const DefaultRenewBefore = 30 * 24 * time.Hour

// EnsureCerts is the orchestrator behind `portal init-certs`. Idempotent
// across concurrent invocations from multiple replicas:
//
//   - Reads the named Secret. If tls.crt + tls.key + ca.crt are all present,
//     the cert chains correctly to the CA, and NotAfter is more than
//     RenewBefore away, no new material is generated.
//   - Otherwise generates a fresh CA + leaf with the supplied DNS SANs and
//     tries to claim the Secret via a ResourceVersion-checked Update (or a
//     Create when the Secret is absent). If another replica wins the race,
//     this replica adopts the winner's material instead of overwriting it
//     — that's what guarantees every pod serves a cert signed by the same
//     CA as the one patched into the webhook's caBundle.
//   - Always patches every entry of the ValidatingWebhookConfiguration's
//     clientConfig.caBundle to the (possibly unchanged) CA. The patch is a
//     no-op when the value is already correct.
//   - Always mirrors the cert material to CertDir, since the main container
//     reads from disk (not from the K8s API).
//
// Returns the resulting CA bundle PEM so callers can verify or log.
func EnsureCerts(ctx context.Context, kube kubernetes.Interface, opts EnsureOptions) ([]byte, error) {
	if opts.Namespace == "" || opts.SecretName == "" || opts.WebhookConfig == "" || opts.Service == "" || opts.CertDir == "" {
		return nil, errors.New("EnsureCerts: namespace, secret, webhook-config, service, cert-dir all required")
	}
	if opts.RenewBefore <= 0 {
		opts.RenewBefore = DefaultRenewBefore
	}

	dnsNames := dnsForService(opts.Service, opts.Namespace, opts.ExtraDNSNames)

	certPEM, keyPEM, caPEM, err := claimOrAdoptSecret(ctx, kube, opts.Namespace, opts.SecretName, dnsNames, opts.RenewBefore)
	if err != nil {
		return nil, fmt.Errorf("claim secret %s/%s: %w", opts.Namespace, opts.SecretName, err)
	}

	if err := patchWebhookCABundle(ctx, kube, opts.WebhookConfig, caPEM); err != nil {
		return nil, fmt.Errorf("patch validatingwebhookconfiguration %q: %w", opts.WebhookConfig, err)
	}
	if err := mirrorToCertDir(opts.CertDir, certPEM, keyPEM, caPEM); err != nil {
		return nil, fmt.Errorf("mirror to %s: %w", opts.CertDir, err)
	}
	return caPEM, nil
}

// claimOrAdoptSecret atomically races to populate the named Secret with
// fresh cert material when the existing content isn't usable. The first
// replica to successfully Update/Create wins; subsequent replicas re-read
// the Secret and adopt the winner's material.
func claimOrAdoptSecret(ctx context.Context, kube kubernetes.Interface, ns, name string, dnsNames []string, renewBefore time.Duration) (cert, key, ca []byte, err error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		existing, err := kube.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, nil, nil, err
		}
		notFound := apierrors.IsNotFound(err)
		if !notFound {
			if c, k, a, valid := parseSecretData(existing.Data, renewBefore); valid {
				return c, k, a, nil
			}
		}

		freshCA, freshCert, freshKey, gerr := generateSelfSigned(dnsNames)
		if gerr != nil {
			return nil, nil, nil, fmt.Errorf("generate: %w", gerr)
		}
		payload := map[string][]byte{
			"tls.crt": freshCert,
			"tls.key": freshKey,
			"ca.crt":  freshCA,
		}

		if notFound {
			_, cerr := kube.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
				Type:       corev1.SecretTypeTLS,
				Data:       payload,
			}, metav1.CreateOptions{})
			if cerr == nil {
				return freshCert, freshKey, freshCA, nil
			}
			if !apierrors.IsAlreadyExists(cerr) {
				return nil, nil, nil, cerr
			}
			continue
		}

		existing.Data = payload
		_, uerr := kube.CoreV1().Secrets(ns).Update(ctx, existing, metav1.UpdateOptions{})
		if uerr == nil {
			return freshCert, freshKey, freshCA, nil
		}
		if !apierrors.IsConflict(uerr) {
			return nil, nil, nil, uerr
		}
	}
	return nil, nil, nil, fmt.Errorf("claim secret %s/%s: exceeded %d attempts", ns, name, maxAttempts)
}

// parseSecretData runs the same shape + chain + expiry checks as
// readExistingSecret but takes raw data so callers that already hold the
// Secret can reuse it without a re-Get.
func parseSecretData(data map[string][]byte, renewBefore time.Duration) (cert, key, ca []byte, valid bool) {
	cert = data["tls.crt"]
	key = data["tls.key"]
	ca = data["ca.crt"]
	if len(cert) == 0 || len(key) == 0 || len(ca) == 0 {
		return nil, nil, nil, false
	}
	if _, err := tls.X509KeyPair(cert, key); err != nil {
		return nil, nil, nil, false
	}
	leaf, err := parseFirstCert(cert)
	if err != nil {
		return nil, nil, nil, false
	}
	caCert, err := parseFirstCert(ca)
	if err != nil {
		return nil, nil, nil, false
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return nil, nil, nil, false
	}
	if time.Until(leaf.NotAfter) < renewBefore {
		return nil, nil, nil, false
	}
	return cert, key, ca, true
}

func parseFirstCert(pemBytes []byte) (*x509.Certificate, error) {
	for {
		block, rest := pem.Decode(pemBytes)
		if block == nil {
			return nil, errors.New("no PEM block")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		pemBytes = rest
	}
}

func dnsForService(svc, ns string, extra []string) []string {
	base := []string{
		svc,
		fmt.Sprintf("%s.%s", svc, ns),
		fmt.Sprintf("%s.%s.svc", svc, ns),
		fmt.Sprintf("%s.%s.svc.cluster.local", svc, ns),
	}
	return append(base, extra...)
}

// patchWebhookCABundle writes caPEM as the caBundle on every webhook entry of
// the named ValidatingWebhookConfiguration. JSON-encoded []byte is
// base64-string in JSON, which is exactly what the API server expects on the
// CABundle field.
func patchWebhookCABundle(ctx context.Context, kube kubernetes.Interface, name string, caPEM []byte) error {
	cur, err := kube.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(cur.Webhooks) == 0 {
		return fmt.Errorf("validatingwebhookconfiguration %q has no webhook entries", name)
	}
	type op struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value []byte `json:"value"`
	}
	ops := make([]op, 0, len(cur.Webhooks))
	for i := range cur.Webhooks {
		ops = append(ops, op{
			Op:    "replace",
			Path:  fmt.Sprintf("/webhooks/%d/clientConfig/caBundle", i),
			Value: caPEM,
		})
	}
	body, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	_, err = kube.AdmissionregistrationV1().ValidatingWebhookConfigurations().Patch(
		ctx, name, types.JSONPatchType, body, metav1.PatchOptions{},
	)
	return err
}

func mirrorToCertDir(dir string, cert, key, ca []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		"tls.crt": cert,
		"tls.key": key,
		"ca.crt":  ca,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
