package main

import (
	"github.com/spf13/cobra"
)

// runOptions holds the flags for `portal run`.
type runOptions struct {
	admission        bool
	audit            bool
	network          bool
	rulesFolder      string
	rulesCR          bool
	kubeconfig       string
	listen           string
	metricsAddr      string
	certDir          string
	webhookDNSNames  []string
	clientCAFile     string
	failClosed       bool
	leaderElection   bool
	installNamespace string
	watchedGvks      []string
	alertmanagerURL  string
	policyReport     bool
}

func newRunCmd() *cobra.Command {
	opts := runOptions{
		admission:        true,
		audit:            false,
		network:          false,
		failClosed:       true,
		leaderElection:   true,
		listen:           ":8443",
		metricsAddr:      ":9090",
		certDir:          "/etc/portal/certs",
		installNamespace: "portal-system",
		rulesCR:          true,
		policyReport:     true,
	}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run Portal (admission webhook, audit loop, network analyser)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPortal(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.admission, "admission", opts.admission, "enable admission webhook layer")
	f.BoolVar(&opts.audit, "audit", opts.audit, "enable informer-driven audit layer")
	f.BoolVar(&opts.network, "network", opts.network, "enable NetworkPolicy analyser layer (implies --audit)")
	f.StringVar(&opts.rulesFolder, "rules-folder", "", "load rules from a filesystem folder (in addition to CRDs)")
	f.BoolVar(&opts.rulesCR, "rules-cr", opts.rulesCR, "load PortalClusterRule/PortalRule CRs")
	f.StringVar(&opts.kubeconfig, "kubeconfig", "", "kubeconfig path (defaults to in-cluster)")
	f.StringVar(&opts.listen, "listen", opts.listen, "TLS webhook listen address")
	f.StringVar(&opts.metricsAddr, "metrics-addr", opts.metricsAddr, "Prometheus /metrics + /healthz listen address")
	f.StringVar(&opts.certDir, "cert-dir", opts.certDir, "directory containing tls.crt / tls.key (auto-generated when missing)")
	f.StringSliceVar(&opts.webhookDNSNames, "webhook-dns", nil, "DNS names to include in the self-signed webhook certificate")
	f.StringVar(&opts.clientCAFile, "client-ca-file", "", "PEM bundle for verifying admission webhook callers (apiserver client CA); empty disables client-cert verification (not recommended in production)")
	f.BoolVar(&opts.failClosed, "fail-closed", opts.failClosed, "advisory: report whether deployment expects failClosed semantics")
	f.BoolVar(&opts.leaderElection, "leader-election", opts.leaderElection, "use lease-based leader election for the audit loop")
	f.StringVar(&opts.installNamespace, "install-namespace", opts.installNamespace, "Portal's own namespace — excluded from the webhook and used as the lease lock namespace")
	f.StringSliceVar(&opts.watchedGvks, "watched-gvk", nil, "additional GVKs to watch (group/version/kind; empty group for core, e.g. /v1/ConfigMap)")
	f.StringVar(&opts.alertmanagerURL, "alertmanager-url", "", "AlertManager v2 alerts endpoint; empty disables the AlertManager sink")
	f.BoolVar(&opts.policyReport, "policy-report", opts.policyReport, "emit wgpolicyk8s.io PolicyReport / ClusterPolicyReport resources")
	return cmd
}
