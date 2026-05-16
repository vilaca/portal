package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"github.com/vilaca/portal/internal/admission"
)

// newInitCertsCmd is the cobra subcommand portal's init-container runs.
//
// It generates (or reuses) the TLS material the admission webhook serves and
// injects the CA bundle into the ValidatingWebhookConfiguration so kube-
// apiserver can validate the webhook's TLS chain. Idempotent: re-running
// against a populated, valid Secret is a no-op except for the (always-applied)
// caBundle patch.
//
// Skipped entirely when the Helm chart's certManager.enabled is true —
// cert-manager + its CA injector handle both Secret population and caBundle
// injection in that mode.
func newInitCertsCmd() *cobra.Command {
	var (
		opts       admission.EnsureOptions
		kubeconfig string
	)
	cmd := &cobra.Command{
		Use:   "init-certs",
		Short: "Generate webhook TLS material and inject the CA bundle into the ValidatingWebhookConfiguration",
		Long: `init-certs is the cert-bootstrap step for Portal installs that do not use
cert-manager. It is invoked from a Pod-level init-container in the Helm chart.

The command is idempotent: if the named Secret already holds a valid cert
that is not within the renewal window, only the ValidatingWebhookConfig's
caBundle is patched (a no-op when already correct). Otherwise a fresh
self-signed CA + leaf are generated, the Secret is written in place, and
the caBundle is patched.

The generated cert is mirrored to --cert-dir so the main container's
filesystem mount reads the new material regardless of kubelet's Secret
volume refresh timing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadRestConfig(kubeconfig)
			if err != nil {
				return fmt.Errorf("kubeconfig: %w", err)
			}
			kube, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				return fmt.Errorf("kube client: %w", err)
			}
			_, err = admission.EnsureCerts(cmd.Context(), kube, opts)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Namespace, "namespace", "portal-system", "namespace containing the TLS Secret")
	f.StringVar(&opts.SecretName, "secret", "portal-webhook-cert", "name of the kubernetes.io/tls Secret to read/upsert")
	f.StringVar(&opts.WebhookConfig, "webhook-config", "portal", "name of the ValidatingWebhookConfiguration to inject caBundle into")
	f.StringVar(&opts.Service, "service", "portal", "Service name; DNS SANs are derived as <svc>.<ns>.svc, <svc>.<ns>.svc.cluster.local")
	f.StringVar(&opts.CertDir, "cert-dir", "/etc/portal/certs", "local path to mirror cert material to (typically a shared emptyDir)")
	f.StringSliceVar(&opts.ExtraDNSNames, "dns", nil, "additional DNS SANs to include on the leaf certificate")
	f.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path (defaults to in-cluster)")
	return cmd
}
