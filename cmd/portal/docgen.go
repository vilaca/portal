package main

import "github.com/spf13/cobra"

// newDocgenCmd renders the cobra command tree as markdown for docs/reference/cli.md.
// Implementation is filled in when Wave 4 lands the docs pipeline.
func newDocgenCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "docgen",
		Short:  "Generate CLI reference markdown",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
