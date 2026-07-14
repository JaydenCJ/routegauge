# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Access-log parsing for nginx/Apache "combined" and "common" formats
  (hand-rolled scanner: escaped quotes in user agents, absolute-form
  proxy targets, `-` sentinels, malformed request lines kept visible as
  `(unparsed)`), including the widespread trailing `$request_time` /
  `$upstream_response_time` extensions.
- JSON-lines parsing driven by documented alias tables: path/method/
  status/bytes/remote aliases, latency fields scaled by unit-suffixed
  names (`duration_ms`, `latency_us`, …), Go duration strings, and
  epoch timestamps scaled by magnitude (s/ms/µs/ns). Format
  auto-detection per file.
- Automatic endpoint clustering in two stages: per-segment heuristics
  (numeric `:id`, `:uuid`, `:hash`, `:date`, `:email`, `:token`,
  extension-aware `:date.csv`, version segments kept literal) plus a
  cardinality pass that collapses any tree position with more distinct
  literals than `--cluster-threshold` into `:param`, merging subtrees.
- Per-route metrics: request counts, distinct raw paths with sample
  paths, 4xx/5xx counts and rates, byte totals, and exact nearest-rank
  p50/p90/p95/p99/avg/max latency percentiles.
- `report` subcommand with terminal gauges, stable JSON
  (`schema_version: 1`), and Markdown output; `endpoints` view showing
  each clustered pattern with samples; `errors` view with a status-code
  histogram and 5xx-first route table.
- `check` subcommand gating on `--max-error-rate`, `--max-5xx-rate`,
  `--max-p95`, `--max-p99` — overall and, with `--per-route`, for every
  route above `--min-requests` — exiting 1 on breach for deploy hooks.
- Input plumbing for the files teams already have: multiple files per
  run, transparent `.gz` rotations, `-` for stdin, 1 MiB line tolerance,
  and `--since`/`--until`/`--method`/`--path-prefix` filters.
- Runnable examples (`examples/make-demo-log.sh`,
  `examples/deploy-gate.sh`) and a clustering-rules reference
  (`docs/clustering.md`).
- 90 deterministic offline tests (unit + in-process CLI integration
  against fabricated logs) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/routegauge/releases/tag/v0.1.0
