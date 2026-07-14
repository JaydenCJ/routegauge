// Markdown output for the report view: paste-ready for a PR comment,
// incident doc, or weekly ops summary.
package render

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/routegauge/internal/stats"
)

// Markdown writes the report as a GitHub-flavored Markdown document.
func Markdown(w io.Writer, rep *stats.Report, top int) {
	fmt.Fprintln(w, "# routegauge report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**%s %s**, %d %s", fmtCount(rep.Requests), plural(rep.Requests, "request"),
		len(rep.Rows), plural(len(rep.Rows), "route"))
	if !rep.First.IsZero() {
		const layout = "2006-01-02 15:04:05 MST"
		fmt.Fprintf(w, " · %s → %s", rep.First.Format(layout), rep.Last.Format(layout))
	}
	if rep.Skipped > 0 {
		fmt.Fprintf(w, " · %d %s skipped", rep.Skipped, plural(rep.Skipped, "line"))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "| Class | Requests | Share |")
	fmt.Fprintln(w, "|---|---:|---:|")
	for _, class := range []string{"1xx", "2xx", "3xx", "4xx", "5xx"} {
		n := rep.Class[class]
		if n == 0 {
			continue
		}
		fmt.Fprintf(w, "| %s | %s | %s |\n",
			class, fmtCount(n), fmtPct(100*float64(n)/float64(rep.Requests)))
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "| Method | Endpoint | Requests | Err% | p50 | p95 | p99 | Max |")
	fmt.Fprintln(w, "|---|---|---:|---:|---:|---:|---:|---:|")
	rows := capRows(rep.Rows, top)
	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s | %s | %s | %s |\n",
			r.Method, r.Route, fmtCount(r.Count), fmtPct(r.ErrRate()),
			fmtDur(r.P50), fmtDur(r.P95), fmtDur(r.P99), fmtDur(r.Max))
	}
	if len(rows) < len(rep.Rows) {
		fmt.Fprintf(w, "\n_%d %s total, top %d shown._\n", len(rep.Rows), plural(len(rep.Rows), "route"), len(rows))
	}
}
