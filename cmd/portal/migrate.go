package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vilaca/portal/internal/rule/migrate"
)

func newMigrateCmd() *cobra.Command {
	var (
		format string
		out    string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "migrate-rules [folder]",
		Short: "Convert podwatcher-poc SpEL rules into Portal expr-lang rules",
		Long: `Rewrites SpEL→expr-lang differences ({...}.contains(x) → x in [...],
.contains('y') → 'y' in ..., filter.namespace → match.namespaces) and emits
either one PortalClusterRule manifest per rule (--format=cr, default) or
folder-format rule YAML (--format=folder). Idempotent on Portal-format input.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			res, err := migrate.Migrate(input, format)
			if err != nil {
				return err
			}

			if dryRun {
				for _, o := range res.Outputs {
					fmt.Fprintf(cmd.OutOrStdout(), "--- %s ---\n%s\n", o.Path, o.Body)
				}
			} else {
				outDir := out
				if outDir == "" {
					outDir = strings.TrimRight(input, string(filepath.Separator)) + "-portal"
				}
				if err := os.MkdirAll(outDir, 0o755); err != nil {
					return fmt.Errorf("mkdir %q: %w", outDir, err)
				}
				for _, o := range res.Outputs {
					full := filepath.Join(outDir, o.Path)
					if err := os.WriteFile(full, o.Body, 0o644); err != nil {
						return fmt.Errorf("write %q: %w", full, err)
					}
				}
			}

			for _, w := range res.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warn: "+w)
			}
			for _, e := range res.Errors {
				fmt.Fprintln(cmd.ErrOrStderr(), "error: "+e)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Migrated %d rule files. %d warnings. %d errors.\n",
				len(res.Outputs), len(res.Warnings), len(res.Errors))
			if len(res.Errors) > 0 {
				return fmt.Errorf("migrate-rules: %d errors", len(res.Errors))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", migrate.FormatCR, "output format: cr | folder")
	cmd.Flags().StringVarP(&out, "output", "o", "", "output directory (default: <input>-portal)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print rewritten content to stdout instead of writing")
	return cmd
}
