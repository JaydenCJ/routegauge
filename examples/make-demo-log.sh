#!/usr/bin/env bash
# Fabricates a deterministic demo access log (combined format with
# nginx $request_time) that exercises every routegauge feature: numeric
# IDs, UUIDs, date-stamped exports, a slow endpoint, 4xx/5xx mixes, one
# garbage line, and one malformed "-" request. Byte-identical on every
# machine — the "randomness" is a fixed linear congruential generator.
#
# usage: bash examples/make-demo-log.sh [outfile]   (default: ./demo-access.log)
set -euo pipefail

OUT="${1:-demo-access.log}"
STATE=20260706

# rnd N: put a deterministic pseudo-random integer in [0, N) into $R.
# A function (not a $(...) subshell) so STATE actually advances.
rnd() {
  STATE=$(((STATE * 1103515245 + 12345) % 2147483648))
  R=$(((STATE / 65536) % $1))
}

# emit MINUTE SECOND METHOD PATH STATUS BYTES LATENCY
emit() {
  printf '127.0.0.1 - - [06/Jul/2026:09:%02d:%02d +0000] "%s %s HTTP/1.1" %s %s "-" "demo-client/1.0" %s\n' \
    "$1" "$2" "$3" "$4" "$5" "$6" "$7" >> "$OUT"
}

: > "$OUT"
for i in $(seq 0 199); do
  minute=$((i * 60 / 200 % 60))
  rnd 60; second=$R
  rnd 20; kind=$R
  case $kind in
    0 | 1 | 2 | 3 | 4 | 5 | 6)  # GET one user — the hot endpoint
      rnd 400; id=$((1000 + R))
      rnd 25; status=200; [ "$R" -eq 0 ] && status=404
      rnd 60; lat="0.0$((12 + R))"
      emit "$minute" "$second" GET "/api/users/$id" $status 512 "$lat"
      ;;
    7 | 8 | 9)                  # a user's orders
      rnd 400; id=$((1000 + R))
      rnd 70; lat="0.0$((25 + R))"
      emit "$minute" "$second" GET "/api/users/$id/orders" 200 2048 "$lat"
      ;;
    10 | 11)                    # create an order; occasionally breaks
      rnd 12; status=201; [ "$R" -eq 0 ] && status=500
      rnd 200; lat="0.$((110 + R))"
      emit "$minute" "$second" POST "/api/orders" $status 128 "$lat"
      ;;
    12 | 13)                    # fetch an order by UUID
      rnd 100000; a=$R
      rnd 65536; b=$R
      rnd 4096; c=$R
      rnd 4096; d=$R
      uuid="$(printf '%08x-%04x-4%03x-8%03x-%012x' "$a" "$b" "$c" "$d" "$((i * 999983))")"
      rnd 40; lat="0.0$((18 + R))"
      emit "$minute" "$second" GET "/api/orders/$uuid" 200 1024 "$lat"
      ;;
    14 | 15)                    # search — slow tail, occasional 500
      rnd 15; status=200; [ "$R" -eq 0 ] && status=500
      rnd 50; q=$R
      rnd 700; lat="0.$((150 + R))"
      emit "$minute" "$second" GET "/api/search?q=term$q" $status 4096 "$lat"
      ;;
    16)                         # date-stamped CSV export — the p95 villain
      rnd 6; day=$((1 + R))
      rnd 700; lat="1.$((200 + R))"
      emit "$minute" "$second" GET "/api/export/2026-07-0$day.csv" 200 65536 "$lat"
      ;;
    17)                         # fingerprinted static asset
      emit "$minute" "$second" GET "/assets/app.3f8a92b1c04d.js" 200 40960 "0.003"
      ;;
    *)                          # load-balancer health checks
      emit "$minute" "$second" GET "/health" 200 2 "0.001"
      ;;
  esac
done
# Two lines a real log always seems to contain: pure garbage (skipped)
# and nginx's "-" request line for a malformed request ((unparsed)).
echo 'garbage that never came from a web server' >> "$OUT"
printf '127.0.0.1 - - [06/Jul/2026:09:59:59 +0000] "-" 400 0 "-" "-" 0.000\n' >> "$OUT"

echo "wrote $(wc -l < "$OUT") lines to $OUT"
