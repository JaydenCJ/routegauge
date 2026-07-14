# routegauge examples

Two runnable scripts, both offline and self-contained.

## make-demo-log.sh

Fabricates a deterministic one-hour access log in combined format with
nginx `$request_time`: a hot `/api/users/<id>` endpoint, UUID order
lookups, a slow search, a very slow date-stamped CSV export, health
checks, an occasional 500, one garbage line (counted as skipped) and
one nginx `"-"` malformed request (reported as `(unparsed)`). The
pseudo-random sequence is a fixed LCG, so the file is byte-identical
on every machine.

```bash
bash examples/make-demo-log.sh /tmp/demo-access.log
routegauge report /tmp/demo-access.log
routegauge endpoints /tmp/demo-access.log
routegauge errors /tmp/demo-access.log
```

## deploy-gate.sh

Shows `routegauge check` as a deploy gate: it exits non-zero when the
error rate or the p95 latency in the freshest log slice exceeds the
budgets your team agreed on, overall and per route. Ready for a deploy
hook or a nightly cron.

```bash
bash examples/deploy-gate.sh /tmp/demo-access.log; echo "exit: $?"
bash examples/deploy-gate.sh /tmp/demo-access.log 2026-07-06T09:30:00Z
```

Both scripts pin timestamps and random sequences, so their output is
identical on every machine.
