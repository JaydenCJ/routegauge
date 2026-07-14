#!/usr/bin/env bash
# routegauge check as a post-deploy gate: tail the freshest slice of the
# access log and fail the pipeline when error rate or latency regressed.
# Wire this into a deploy hook or a nightly cron; exit code 1 = breach.
#
# usage: bash examples/deploy-gate.sh <access.log> [since-RFC3339]
set -euo pipefail

LOG="${1:?usage: deploy-gate.sh <access.log> [since-RFC3339]}"
SINCE="${2:-}"
# Uses routegauge from PATH; point ROUTEGAUGE at a local build otherwise
# (e.g. ROUTEGAUGE=./routegauge bash examples/deploy-gate.sh access.log).
ROUTEGAUGE="${ROUTEGAUGE:-routegauge}"

ARGS=(check
  --max-error-rate 5   # 4xx+5xx above 5% fails the deploy
  --max-5xx-rate 1     # 5xx above 1% fails it faster
  --max-p95 800ms      # overall latency budget
  --per-route          # ... and no single route may breach it either
  --min-requests 20    # ignore routes without a meaningful sample
)
[ -n "$SINCE" ] && ARGS+=(--since "$SINCE")

"$ROUTEGAUGE" "${ARGS[@]}" "$LOG"
