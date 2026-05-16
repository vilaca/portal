// Command metricsdoc prints the Portal Prometheus metrics registry as a
// Markdown table. The Makefile generate-docs target writes the output to
// docs/reference/metrics.md; CI compares against the committed file to
// detect drift.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	prom "github.com/vilaca/portal/internal/sink/prometheus"
)

func main() {
	if err := write(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// write renders the Markdown table. Exported (lower-case but package main —
// inlined in test via build tag would be heavier; tests call render() below).
func write(w io.Writer) error {
	_, err := io.WriteString(w, Render())
	return err
}

// Render returns the markdown body for the registry. Kept as a pure string
// builder so tests can assert non-empty output without spawning a process.
func Render() string {
	var b strings.Builder
	b.WriteString("# Portal Prometheus metrics\n\n")
	b.WriteString("Generated from `internal/sink/prometheus` — do not edit by hand.\n\n")
	b.WriteString("| Name | Type | Labels | Description |\n")
	b.WriteString("|------|------|--------|-------------|\n")
	for _, m := range prom.Registry() {
		labels := "—"
		if len(m.Labels) > 0 {
			labels = "`" + strings.Join(m.Labels, "`, `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", m.Name, m.Type, labels, m.Description)
	}
	return b.String()
}
