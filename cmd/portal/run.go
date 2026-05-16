package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// runOptions holds the flags for `portal run`.
type runOptions struct {
	admission   bool
	audit       bool
	network     bool
	rulesFolder string
	rulesCR     bool
	kubeconfig  string
	listen      string
	metricsAddr string
	failClosed  bool
	watchedGvks []string
}

func newRunCmd() *cobra.Command {
	opts := runOptions{
		admission:   true,
		audit:       false,
		network:     false,
		failClosed:  true,
		listen:      ":8443",
		metricsAddr: ":9090",
		rulesCR:     true,
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
	f.BoolVar(&opts.network, "network", opts.network, "enable NetworkPolicy analyser layer")
	f.StringVar(&opts.rulesFolder, "rules-folder", "", "load rules from a filesystem folder (in addition to CRDs)")
	f.BoolVar(&opts.rulesCR, "rules-cr", opts.rulesCR, "load PortalClusterRule/PortalRule CRs")
	f.StringVar(&opts.kubeconfig, "kubeconfig", "", "kubeconfig path (defaults to in-cluster)")
	f.StringVar(&opts.listen, "listen", opts.listen, "TLS webhook listen address")
	f.StringVar(&opts.metricsAddr, "metrics-addr", opts.metricsAddr, "Prometheus /metrics + /healthz listen address")
	f.BoolVar(&opts.failClosed, "fail-closed", opts.failClosed, "advisory: report whether deployment expects failClosed semantics")
	f.StringSliceVar(&opts.watchedGvks, "watched-gvk", nil, "additional GVKs to watch (group/version/kind)")
	return cmd
}

// runPortal is the entry point invoked by `portal run`. The full composition root
// is intentionally small: it constructs each enabled layer, hands it the registries,
// and blocks until Start returns. The heavy lifting lives in the internal/* modules.
func runPortal(ctx interface{}, opts runOptions) error {
	// The wire-up of admission / audit / network / actions / sinks lives in cmd/portal/wire.go
	// once Wave 3 completes. Until then, `run` prints the resolved configuration so the
	// binary stays useful for smoke-testing the rest of the toolchain.
	fmt.Printf("portal run: admission=%v audit=%v network=%v rulesFolder=%q rulesCR=%v listen=%q metrics=%q failClosed=%v\n",
		opts.admission, opts.audit, opts.network, opts.rulesFolder, opts.rulesCR, opts.listen, opts.metricsAddr, opts.failClosed)
	return nil
}
