package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var (
		format string
		out    string
	)
	cmd := &cobra.Command{
		Use:   "migrate-rules [folder]",
		Short: "Convert podwatcher-poc SpEL rules into Portal expr-lang rules",
		Long: `Rewrites SpEL→expr-lang differences ({...}.contains(x) → x in [...],
.contains('y') → 'y' in ..., filter.namespace → match.namespaces). Idempotent.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Real implementation lives in internal/rule/migrate (added in Wave 3).
			return fmt.Errorf("migrate-rules: %s -> %s (format=%s) — implementation lands in Wave 3", args[0], out, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "cr", "output format: cr | folder")
	cmd.Flags().StringVarP(&out, "output", "o", "", "output directory (default: alongside input)")
	return cmd
}
