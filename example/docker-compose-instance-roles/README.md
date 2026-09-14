# Instance-role docker-compose example

Demonstrates the `--role` instance-role split (see the main
[README.md](../../README.md#instance-roles)) against a real target
Prometheus:

- **3x `scrape-target`** — Prometheus scrapes their `/metrics`.
- **2x `remote-writer`** — push samples to Prometheus via `remote_write`.
- **1x `ruler`** — generates recording/alerting rules, served on `/rules`;
  fetched once at startup into Prometheus's `rule_files`.

All six avalanche instances share the exact same metric-generation config
(~100,000 series each: 500 gauges + 500 counters, 100 series each) via the
`AVALANCHE_*` environment anchor in [docker-compose.yml](docker-compose.yml)
— only `--role` (and, for the remote-writers, `AVALANCHE_REMOTE_URL` /
`AVALANCHE_CONST_LABEL`) differs per instance.

## Run it

```bash
docker compose up --build
```

Then:
- Prometheus UI: http://localhost:9090
- Scrape targets directly: http://localhost:9001/metrics, `:9002`, `:9003`
- Ruler's raw rules: http://localhost:9004/rules

## Things that aren't obvious from the flags alone

- **No series sharding.** Each `scrape-target`/`remote-writer` replica
  independently generates and exposes the *same* ~100,000 series (redundant,
  not partitioned) — there's no `--shard-index`/`--shard-count` in this
  build (see `openspec/changes/add-instance-roles/design.md` Risks). Don't
  read "3 scrape-targets" as "3x the unique series".
- **`--remote-url` must NOT include `/api/v1/write`.** avalanche's writer
  appends that path itself; giving it a URL that already ends in
  `/api/v1/write` produces a doubled, 404-ing path. Use the bare Prometheus
  base URL (`http://prometheus:9090`).
- **Each remote-writer needs a distinguishing label.** With identical config
  and no `AVALANCHE_CONST_LABEL`, both writers generate the exact same series
  and push conflicting values for the same timestamp — Prometheus's
  remote-write receiver rejects that as a duplicate sample (HTTP 400).
- **`AVALANCHE_REMOTE_OUT_OF_ORDER=false`.** avalanche's writer intentionally
  sends some out-of-order timestamps by default (to load-test OOO handling);
  this target Prometheus has no `--storage.tsdb.out-of-order-time-window`
  configured, so it rejects those (HTTP 400) and avalanche gives up
  ("too many errors") after enough of them, exiting the whole container. Set
  it back to `true` (and give Prometheus an OOO window) if that's what you
  actually want to exercise.
- **`AVALANCHE_REMOTE_REQUESTS_COUNT=-1`.** The default (`100`) makes
  remote-write a bounded one-off run; once it finishes, avalanche's
  `run.Group` tears down the *whole* process (not just the writer), and the
  container exits. `-1` keeps it running indefinitely, as you'd want for a
  long-running instance.
- **Memory.** ~100,000 series costs roughly 150-250MB RSS per avalanche
  instance in practice. Scale `AVALANCHE_GAUGE_METRIC_COUNT` /
  `AVALANCHE_COUNTER_METRIC_COUNT` / `AVALANCHE_SERIES_COUNT` up or down to
  fit your machine — remember every replica of a role pays that cost
  independently (no sharding).
