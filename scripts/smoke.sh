#!/usr/bin/env bash
# End-to-end smoke test for routegauge: builds the binary, fabricates a
# deterministic access-log rotation set (plain + gzip + JSON lines), and
# asserts on the real CLI output. No network, idempotent, finishes in
# seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/routegauge"
LOG="$WORKDIR/access.log"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/routegauge) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "routegauge 0.1.0" || fail "--version mismatch"

echo "3. fabricate a rotated access-log set"
{
  for i in 0 1 2 3 4 5 6 7; do
    printf '127.0.0.1 - - [06/Jul/2026:09:00:0%d +0000] "GET /api/users/%d HTTP/1.1" 200 512 "-" "smoke/1.0" 0.020\n' "$i" $((1000 + i))
  done
  printf '127.0.0.1 - - [06/Jul/2026:09:00:08 +0000] "GET /api/users/42 HTTP/1.1" 500 64 "-" "smoke/1.0" 0.900\n'
  printf '127.0.0.1 - - [06/Jul/2026:09:00:09 +0000] "POST /api/orders HTTP/1.1" 201 128 "-" "smoke/1.0" 0.150\n'
  printf '127.0.0.1 - - [06/Jul/2026:09:00:10 +0000] "GET /health HTTP/1.1" 200 2 "-" "smoke/1.0" 0.001\n'
  printf 'not an access log line\n'
} > "$LOG"
printf '127.0.0.1 - - [05/Jul/2026:23:59:59 +0000] "GET /api/users/7 HTTP/1.1" 404 0 "-" "smoke/1.0" 0.010\n' \
  | gzip > "$LOG.1.gz"

echo "4. report clusters /api/users/<n> into /api/users/:id"
OUT="$("$BIN" report "$LOG" "$LOG.1.gz")"
echo "$OUT" | grep -q "12 requests" || fail "request total wrong (want 12 across both files)"
echo "$OUT" | grep -q "/api/users/:id" || fail "numeric IDs not clustered"
echo "$OUT" | grep -q "skipped: 1 unparseable line" || fail "skipped-line accounting missing"
echo "$OUT" | grep -q "█" || fail "status gauge missing"
if echo "$OUT" | grep -q "/api/users/1000"; then
  fail "raw path leaked into clustered report"
fi

echo "5. JSON report is machine-readable and versioned"
JSON="$("$BIN" report --format json "$LOG" "$LOG.1.gz")"
echo "$JSON" | grep -q '"tool": "routegauge"' || fail "json envelope missing"
echo "$JSON" | grep -q '"schema_version": 1' || fail "schema_version missing"
echo "$JSON" | grep -q '"route": "/api/users/:id"' || fail "clustered route missing from json"
echo "$JSON" | grep -q '"p95_ms": 900' || fail "p95 wrong in json"

echo "6. endpoints view keeps sample raw paths"
"$BIN" endpoints "$LOG" | grep -q "e.g. /api/users/1000" \
  || fail "sample paths missing from endpoints view"

echo "7. errors view isolates the failing routes"
ERRS="$("$BIN" errors "$LOG" "$LOG.1.gz")"
echo "$ERRS" | grep -q "2 error responses" || fail "error total wrong"
echo "$ERRS" | grep -q "500" || fail "500 histogram row missing"
if echo "$ERRS" | grep -q "/health"; then
  fail "clean route leaked into errors view"
fi

echo "8. check gates with exit codes"
"$BIN" check --max-error-rate 50 "$LOG" "$LOG.1.gz" >/dev/null \
  || fail "check should pass at 50% limit"
if "$BIN" check --max-error-rate 5 "$LOG" "$LOG.1.gz" >/dev/null; then
  fail "check should breach at 5% limit (share is 16.7%)"
fi

echo "9. stdin and JSON-lines auto-detection"
printf '{"time":"2026-07-06T09:00:00Z","method":"GET","path":"/items/9","status":200,"duration_ms":12}\n' \
  | "$BIN" report - | grep -q "/items/:id" || fail "jsonl-over-stdin not detected"

echo "10. usage errors exit 2"
set +e
"$BIN" report --format yaml "$LOG" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
set -e

echo "SMOKE OK"
