// Terminal renderers: fixed-layout tables with unicode gauges, built
// for humans reading a report in a terminal or pasting it into chat.
// All output is byte-deterministic for a given Report.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/JaydenCJ/routegauge/internal/stats"
)

// Text writes the main report: totals, status-class gauge, and the
// per-endpoint table limited to top rows (0 = all).
func Text(w io.Writer, rep *stats.Report, top int) {
	fmt.Fprintf(w, "routegauge report — %s %s, %d %s\n",
		fmtCount(rep.Requests), plural(rep.Requests, "request"),
		len(rep.Rows), plural(len(rep.Rows), "route"))
	writeWindow(w, rep)
	fmt.Fprintln(w)
	writeClassLine(w, rep)
	fmt.Fprintln(w)

	rows := capRows(rep.Rows, top)
	width := routeWidth(rows)
	fmt.Fprintf(w, "%-7s %-*s %9s %7s %8s %8s %8s %8s\n",
		"method", width, "endpoint", "requests", "err%", "p50", "p95", "p99", "max")
	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(w, "%-7s %-*s %9s %7s %8s %8s %8s %8s\n",
			r.Method, width, r.Route, fmtCount(r.Count), fmtPct(r.ErrRate()),
			fmtDur(r.P50), fmtDur(r.P95), fmtDur(r.P99), fmtDur(r.Max))
	}
	fmt.Fprintln(w)
	if len(rows) < len(rep.Rows) {
		fmt.Fprintf(w, "%d %s total (top %d shown)", len(rep.Rows), plural(len(rep.Rows), "route"), len(rows))
	} else {
		fmt.Fprintf(w, "%d %s total", len(rep.Rows), plural(len(rep.Rows), "route"))
	}
	if rep.HasLatency {
		fmt.Fprintf(w, ", overall p95 %s", fmtDur(rep.Overall.P95))
	}
	fmt.Fprintln(w)
	if !rep.HasLatency {
		fmt.Fprintln(w, "note: no latency field in these logs — see README \"Getting latency into your logs\"")
	}
}

// Endpoints writes the clustering-focused view: every route pattern
// with its request count, distinct raw paths, and sample paths.
func Endpoints(w io.Writer, rep *stats.Report, top int) {
	fmt.Fprintf(w, "routegauge endpoints — %d route %s, %s %s\n",
		len(rep.Rows), plural(len(rep.Rows), "pattern"),
		fmtCount(rep.Requests), plural(rep.Requests, "request"))
	writeWindow(w, rep)
	fmt.Fprintln(w)
	rows := capRows(rep.Rows, top)
	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(w, "%-7s %s\n", r.Method, r.Route)
		fmt.Fprintf(w, "        %s %s, %s distinct %s",
			fmtCount(r.Count), plural(r.Count, "request"),
			fmtCount(r.DistinctPaths), plural(r.DistinctPaths, "path"))
		if r.DistinctPaths > 1 && len(r.Samples) > 0 {
			fmt.Fprintf(w, " — e.g. %s", strings.Join(r.Samples, ", "))
		}
		fmt.Fprintln(w)
	}
	if len(rows) < len(rep.Rows) {
		fmt.Fprintf(w, "\n%d route %s total (top %d shown)\n", len(rep.Rows), plural(len(rep.Rows), "pattern"), len(rows))
	}
}

// Errors writes the error-focused view: status-code histogram plus the
// routes producing errors, sorted by 5xx first.
func Errors(w io.Writer, rep *stats.Report, top int) {
	errTotal := rep.Overall.C4xx + rep.Overall.C5xx
	fmt.Fprintf(w, "routegauge errors — %s error %s, %s of %s %s\n",
		fmtCount(errTotal), plural(errTotal, "response"),
		fmtPct(rep.Overall.ErrRate()), fmtCount(rep.Requests), plural(rep.Requests, "request"))
	writeWindow(w, rep)
	fmt.Fprintln(w)
	if errTotal == 0 {
		fmt.Fprintln(w, "no 4xx or 5xx responses in this window")
		return
	}

	fmt.Fprintln(w, "status codes")
	codes, maxN := errorCodes(rep.Overall.Statuses)
	for _, c := range codes {
		n := rep.Overall.Statuses[c]
		fmt.Fprintf(w, "  %d  %s %s\n", c, gauge(float64(n)/float64(maxN), 16), fmtCount(n))
	}
	fmt.Fprintln(w)

	var rows []stats.Row
	for _, r := range rep.Rows {
		if r.C4xx+r.C5xx > 0 {
			rows = append(rows, r)
		}
	}
	sortByErrors(rows)
	rows = capRows(rows, top)
	width := routeWidth(rows)
	fmt.Fprintf(w, "%-7s %-*s %9s %7s %7s %7s   %s\n",
		"method", width, "endpoint", "requests", "4xx", "5xx", "err%", "top status")
	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(w, "%-7s %-*s %9s %7s %7s %7s   %s\n",
			r.Method, width, r.Route, fmtCount(r.Count), fmtCount(r.C4xx),
			fmtCount(r.C5xx), fmtPct(r.ErrRate()), topErrorStatus(r.Statuses))
	}
}

// writeWindow prints the observed time range, when the logs carried
// timestamps at all.
func writeWindow(w io.Writer, rep *stats.Report) {
	if rep.First.IsZero() {
		return
	}
	const layout = "2006-01-02 15:04:05 MST"
	fmt.Fprintf(w, "window: %s → %s\n", rep.First.Format(layout), rep.Last.Format(layout))
	if rep.Skipped > 0 {
		fmt.Fprintf(w, "skipped: %d unparseable %s\n", rep.Skipped, plural(rep.Skipped, "line"))
	}
}

// writeClassLine prints the 2xx gauge plus compact shares per class.
func writeClassLine(w io.Writer, rep *stats.Report) {
	total := rep.Requests
	if total == 0 {
		return
	}
	share := func(class string) float64 {
		return float64(rep.Class[class]) / float64(total)
	}
	fmt.Fprintf(w, "status  2xx %s %s", gauge(share("2xx"), 24), fmtPct(100*share("2xx")))
	for _, class := range []string{"1xx", "3xx", "4xx", "5xx"} {
		if rep.Class[class] > 0 {
			fmt.Fprintf(w, "   %s %s", class, fmtPct(100*share(class)))
		}
	}
	fmt.Fprintln(w)
}

// errorCodes returns the 4xx/5xx codes present, 5xx first then by
// count, plus the maximum count for gauge scaling.
func errorCodes(statuses map[int]int) ([]int, int) {
	var codes []int
	maxN := 1
	for c, n := range statuses {
		if c >= 400 {
			codes = append(codes, c)
			if n > maxN {
				maxN = n
			}
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		a5, b5 := codes[i] >= 500, codes[j] >= 500
		if a5 != b5 {
			return a5
		}
		if statuses[codes[i]] != statuses[codes[j]] {
			return statuses[codes[i]] > statuses[codes[j]]
		}
		return codes[i] < codes[j]
	})
	return codes, maxN
}

// topErrorStatus formats the most frequent error code, e.g. "500×9".
func topErrorStatus(statuses map[int]int) string {
	best, bestN := 0, 0
	for c, n := range statuses {
		if c >= 400 && (n > bestN || (n == bestN && c < best)) {
			best, bestN = c, n
		}
	}
	if best == 0 {
		return "—"
	}
	return fmt.Sprintf("%d×%d", best, bestN)
}

// sortByErrors orders rows by 5xx count, then total errors, then route.
func sortByErrors(rows []stats.Row) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := &rows[i], &rows[j]
		if a.C5xx != b.C5xx {
			return a.C5xx > b.C5xx
		}
		if a.C4xx != b.C4xx {
			return a.C4xx > b.C4xx
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		return a.Method < b.Method
	})
}

func capRows(rows []stats.Row, top int) []stats.Row {
	if top > 0 && len(rows) > top {
		return rows[:top]
	}
	return rows
}

// routeWidth sizes the endpoint column to its longest visible route.
func routeWidth(rows []stats.Row) int {
	width := 24
	for i := range rows {
		if len(rows[i].Route) > width {
			width = len(rows[i].Route)
		}
	}
	return width
}

// gauge renders a fraction as a fixed-width bar: █ filled, ░ empty.
func gauge(frac float64, width int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// plural appends "s" to a unit noun when n != 1.
func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}

// fmtDur renders seconds human-first: sub-10ms with a decimal, whole
// milliseconds below a second, seconds above. -1 renders as an em dash.
func fmtDur(sec float64) string {
	if sec < 0 {
		return "—"
	}
	ms := sec * 1000
	switch {
	case ms < 1:
		return fmt.Sprintf("%.2fms", ms)
	case ms < 10:
		return fmt.Sprintf("%.1fms", ms)
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case sec < 60:
		return fmt.Sprintf("%.2fs", sec)
	default:
		return fmt.Sprintf("%.1fs", sec)
	}
}

func fmtPct(p float64) string {
	return fmt.Sprintf("%.1f%%", p)
}

// fmtCount adds thousands separators: 12847 → "12,847".
func fmtCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
