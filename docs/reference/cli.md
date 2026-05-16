# CLI reference

> Auto-generated from cobra `--help` output. Run `go run ./cmd/portal --help` to refresh.

## `portal`

```text
Portal evaluates expr-lang rules against Kubernetes resources at admission time
and continuously over informer-driven audit, dispatches response actions, and analyses
NetworkPolicy graphs declaratively.

Usage:
  portal [command]

Available Commands:
  completion    Generate the autocompletion script for the specified shell
  help          Help about any command
  migrate-rules Convert podwatcher-poc SpEL rules into Portal expr-lang rules
  run           Run Portal (admission webhook, audit loop, network analyser)

Flags:
  -h, --help      help for portal
  -v, --version   version for portal

Use "portal [command] --help" for more information about a command.
```

## `portal run`

```text
Run Portal (admission webhook, audit loop, network analyser)

Usage:
  portal run [flags]

Flags:
      --admission                  enable admission webhook layer (default true)
      --alertmanager-url string    AlertManager v2 alerts endpoint; empty disables the AlertManager sink
      --audit                      enable informer-driven audit layer
      --cert-dir string            directory containing tls.crt / tls.key (auto-generated when missing) (default "/etc/portal/certs")
      --fail-closed                advisory: report whether deployment expects failClosed semantics (default true)
  -h, --help                       help for run
      --install-namespace string   Portal's own namespace — excluded from the webhook and used as the lease lock namespace (default "portal-system")
      --kubeconfig string          kubeconfig path (defaults to in-cluster)
      --leader-election            use lease-based leader election for the audit loop (default true)
      --listen string              TLS webhook listen address (default ":8443")
      --metrics-addr string        Prometheus /metrics + /healthz listen address (default ":9090")
      --network                    enable NetworkPolicy analyser layer (implies --audit)
      --policy-report              emit wgpolicyk8s.io PolicyReport / ClusterPolicyReport resources (default true)
      --rules-cr                   load PortalClusterRule/PortalRule CRs (default true)
      --rules-folder string        load rules from a filesystem folder (in addition to CRDs)
      --watched-gvk strings        additional GVKs to watch (group/version/kind; empty group for core, e.g. /v1/ConfigMap)
      --webhook-dns strings        DNS names to include in the self-signed webhook certificate
```

## `portal migrate-rules`

```text
Rewrites SpEL→expr-lang differences ({...}.contains(x) → x in [...],
.contains('y') → 'y' in ..., filter.namespace → match.namespaces) and emits
either one PortalClusterRule manifest per rule (--format=cr, default) or
folder-format rule YAML (--format=folder). Idempotent on Portal-format input.

Usage:
  portal migrate-rules [folder] [flags]

Flags:
      --dry-run         print rewritten content to stdout instead of writing
      --format string   output format: cr | folder (default "cr")
  -h, --help            help for migrate-rules
  -o, --output string   output directory (default: <input>-portal)
```
