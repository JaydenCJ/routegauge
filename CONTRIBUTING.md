# Contributing to routegauge

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — the tool and its tests are stdlib-only.

```bash
git clone https://github.com/JaydenCJ/routegauge && cd routegauge
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, fabricates a deterministic rotated
log set (plain + gzip + JSON lines) in a temp dir, and asserts on real
CLI output across every subcommand; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsers, clustering, and stats never touch the filesystem —
   only `logread` and the CLI shell do).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR. routegauge reads files you point it at and writes to stdout —
  no network calls, ever, and no telemetry.
- Clustering rules are data with receipts: a new segment heuristic goes
  into `internal/cluster/segment.go` with a test for the rule *and* a
  test for its nearest near-miss that must stay literal, plus a row in
  `docs/clustering.md`.
- New log-format fields extend the alias tables in
  `internal/parse/jsonl.go`, never a per-vendor code path.
- Code comments and doc comments are written in English.
- Determinism first: identical input must produce byte-identical
  reports, including all orderings and tie-breaks.

## Reporting bugs

Include the output of `routegauge version`, the full command you ran,
and 2–3 sanitized log lines that reproduce the problem — the parsers
are line-oriented, so a single offending line is usually a complete
repro. For clustering complaints, `routegauge endpoints` output showing
the mis-grouped routes says it all.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
