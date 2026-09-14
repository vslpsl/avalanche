# avalanche

Avalanche is a load-testing binary capable of generating metrics that can be either:

* scraped via [Prometheus scrape formats](https://prometheus.io/docs/instrumenting/exposition_formats/) (including [OpenMetrics](https://github.com/OpenObservability/OpenMetrics)) endpoint.
* written via Prometheus Remote Write (v1 only for now) to a target endpoint.

This allows load testing services that can scrape (e.g. Prometheus, OpenTelemetry Collector and so), as well as, services accepting data via Prometheus remote_write API such as [Thanos](https://github.com/thanos-io/thanos), [Cortex](https://github.com/cortexproject/cortex), [M3DB](https://m3db.github.io/m3/integrations/prometheus/), [VictoriaMetrics](https://github.com/VictoriaMetrics/VictoriaMetrics/) and other services [listed here](https://prometheus.io/docs/operating/integrations/#remote-endpoints-and-storage).

Metric names and unique series change over time to simulate series churn.

In addition to the full series churn driven by `--series-interval`, a
separate, independent mechanism lets you simulate gradual, partial churn of
a fraction of series over a longer window (e.g. matching a Prometheus TSDB
block duration) — see `--partial-series-churn-interval` (default `7200`s),
`--partial-series-churn-percent` (default `0`, disabled) and
`--partial-series-churn-step` (default `30`s) in `--help`.

Checkout the (old-ish) [blog post](https://blog.freshtracks.io/load-testing-prometheus-metric-ingestion-5b878711711c).

## Installing

### Locally

```bash
go install github.com/prometheus-community/avalanche/cmd/avalanche@latest
${GOPATH}/bin/avalanche --help
```

### TLS flags for remote write

| Flag | Default | Description |
|------|---------|-------------|
| `--tls-client-insecure` | `false` | Skip server certificate verification |
| `--tls-client-cert-file` | `""` | Path to PEM-encoded client certificate (mTLS) |
| `--tls-client-key-file` | `""` | Path to PEM-encoded client private key (mTLS) |
| `--tls-ca-cert-file` | `""` | Path to PEM-encoded CA certificate to verify the server |

`--tls-client-cert-file` and `--tls-client-key-file` must be set together or not at all.

Example with mTLS:

```bash
avalanche --remote-url=https://example.com/api/v1/push \
  --tls-client-cert-file=client.crt \
  --tls-client-key-file=client.key \
  --tls-ca-cert-file=ca.crt
```

### Docker 

```bash
docker run quay.io/prometheuscommunity/avalanche:latest --help
```

NOTE: We recommend using pinned image to a certain version (see all tags [here](https://quay.io/repository/prometheuscommunity/avalanche?tab=tags&tag=latest))

## Using

See [example](example/kubernetes-deployment.yaml) k8s manifest for deploying avalanche as an always running scrape target.

### Configuration

See `--help` for all flags and their documentation.

Notably, from 0.6.0 version, `avalanche` allows specifying various counts per various metric types.

You can choose you own distribution, but usually it makes more sense to mimic realistic distribution used by your example targets. Feel free to use a [handy `mtypes` Go CLI](./cmd/mtypes) to gather type distributions from a target and generate avalanche flags from it.

On top of scrape target functionality, avalanche is capable of Remote Write client load simulation, following the same, configured metric distribution via `--remote*` flags.

Series values are generated using a random source seeded from the current time by default; pass `--seed` (non-zero) to make the sequence of generated values reproducible between runs with an identical config.

avalanche can also generate Prometheus recording and alerting rules matching its own metric configuration, served over HTTP (see `/rules` below) — see `--recording-rule-count`, `--alerting-rule-count`, `--rule-group-size` and `--rule-eval-interval` in `--help`. The rule set is built once at startup and cached — it does not track subsequent series churn or `--metric-interval` renames. Alerting thresholds are simple heuristics over the `[0,99)` range every metric type is generated with (see `--help`) — treat them as a starting point to tune for your own scenario, not a guarantee of a "meaningful" alert (for example, with the default histogram bucket layout, the generated histogram alert is expected to fire close to continuously).

#### Instance roles

By default (`--role=""`) a single avalanche process runs every subsystem, each gated only by its own trigger flag, exactly as before `--role` existed. Pass `--role` to instead run the process as exactly one subsystem, ignoring every other subsystem's trigger flags even if they're set:

* `--role=scrape-target` — only `/metrics` (+ `/health`). No remote-write, no rule generation/serving.
* `--role=remote-writer` — only remote-write (+ `/health`). Requires `--remote-url`. No `/metrics`, no rules.
* `--role=ruler` — only generates and serves rules on `--rules-endpoint-path` (+ `/health`). Requires a non-empty `--rules-endpoint-path`. Doesn't create the series generator at all (saves the memory/CPU a `Collector` would otherwise use).

This lets you split one avalanche configuration across specialized instances (e.g. several `scrape-target` pods, a couple of `remote-writer` pods, and a single `ruler` pod), all sharing the exact same metric/rule configuration and differing only in `--role`.

To make sharing that configuration across many instances easy, every flag (from `--help`) can also be set via an environment variable `AVALANCHE_<FLAG_NAME>` (dashes become underscores, e.g. `--series-count` ↔ `AVALANCHE_SERIES_COUNT`, `--rules-endpoint-path` ↔ `AVALANCHE_RULES_ENDPOINT_PATH`); an explicit CLI flag always overrides the environment variable. The repeatable `--const-label` flag is the one exception: via its env var it only accepts multiple `label=value` pairs as a single string joined by newlines (`\n`), not as CSV or a repeated variable.

A typical Kubernetes deployment: one `ConfigMap` holding the shared `AVALANCHE_*` config (metric/series/label counts, `AVALANCHE_RULES_ENDPOINT_PATH`, etc.), mounted via `envFrom` into three Deployments that differ only in `AVALANCHE_ROLE`/`--role`: N replicas with `scrape-target`, M replicas with `remote-writer` (plus their own `AVALANCHE_REMOTE_URL`), and 1 replica with `ruler`.

#### Config file

Flags can also come from a YAML file: `--config-file=path/to/config.yaml` (or `AVALANCHE_CONFIG_FILE`), keyed by flag name without the leading `--`:

```yaml
gauge-metric-count: 500
counter-metric-count: 500
series-count: 1000
label-count: 10
role: scrape-target
const-label:       # repeatable flags take a YAML list
  - team=avalanche
  - env=staging
```

Precedence is `CLI flag > env var > config file > built-in default` — a value from the file only takes effect where neither an explicit flag nor an env var set it. An unknown key in the file is a startup error.

### Endpoints

Three endpoints are available :
* `/metrics` - metrics endpoint
* `/health` - healthcheck endpoint
* `/rules` - generated Prometheus rule groups (recording + alerting), as YAML; enabled by default, disable with `--rules-endpoint-path=""`. Example:
  ```bash
  curl http://localhost:9001/rules > rules.yml
  # then add rules.yml to rule_files: in your target Prometheus and reload it.
  ```
