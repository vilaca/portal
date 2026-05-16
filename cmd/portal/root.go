package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "portal",
		Short: "Portal — Kubernetes admission, audit, NetworkPolicy and response-action engine",
		Long: `Portal evaluates expr-lang rules against Kubernetes resources at admission time
and continuously over informer-driven audit, dispatches response actions, and analyses
NetworkPolicy graphs declaratively.`,
		SilenceUsage: true,
		Version:      version,
	}
	root.AddCommand(newRunCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newDocgenCmd())
	return root
}
