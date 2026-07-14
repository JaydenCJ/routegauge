// The check subcommand: a deploy/cron gate. It evaluates error-rate and
// latency limits against the overall aggregate (and, with --per-route,
// against every route with enough traffic) and exits 1 on any breach —
// small enough to run from a post-deploy hook or a nightly cron.
package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/JaydenCJ/routegauge/internal/stats"
)

// checkLimits holds the resolved gate configuration. Negative values
// mean "not enforced".
type checkLimits struct {
	maxErrorRate float64
	max5xxRate   float64
	maxP95       float64 // seconds
	maxP99       float64 // seconds
	perRoute     bool
	minRequests  int
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("check", stderr)
	var af analysisFlags
	af.register(fs)
	maxErrorRate := fs.Float64("max-error-rate", -1, "max 4xx+5xx share, percent")
	max5xxRate := fs.Float64("max-5xx-rate", -1, "max 5xx share, percent")
	maxP95 := fs.String("max-p95", "", "max p95 latency (Go duration, e.g. 250ms)")
	maxP99 := fs.String("max-p99", "", "max p99 latency (Go duration, e.g. 1.5s)")
	perRoute := fs.Bool("per-route", false, "also enforce limits on every route")
	minRequests := fs.Int("min-requests", 10, "per-route minimum requests before limits apply")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if err := af.validate(); err != nil {
		fmt.Fprintf(stderr, "routegauge check: %v\n", err)
		return ExitUsage
	}

	limits := checkLimits{
		maxErrorRate: *maxErrorRate,
		max5xxRate:   *max5xxRate,
		maxP95:       -1,
		maxP99:       -1,
		perRoute:     *perRoute,
		minRequests:  *minRequests,
	}
	var err error
	if limits.maxP95, err = parseLimitDuration(*maxP95); err != nil {
		fmt.Fprintf(stderr, "routegauge check: invalid --max-p95: %v\n", err)
		return ExitUsage
	}
	if limits.maxP99, err = parseLimitDuration(*maxP99); err != nil {
		fmt.Fprintf(stderr, "routegauge check: invalid --max-p99: %v\n", err)
		return ExitUsage
	}
	if !limits.any() {
		fmt.Fprintln(stderr, "routegauge check: no limits given (set --max-error-rate, --max-5xx-rate, --max-p95, or --max-p99)")
		return ExitUsage
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(stderr, "routegauge check: no input files (pass one or more log files, or - for stdin)")
		return ExitUsage
	}

	rep, err := analyze(files, &af, "requests")
	if err != nil {
		fmt.Fprintf(stderr, "routegauge check: %v\n", err)
		return ExitRuntime
	}
	if evaluate(stdout, rep, limits) {
		fmt.Fprintln(stdout, "check: FAIL")
		return ExitBreach
	}
	fmt.Fprintln(stdout, "check: OK")
	return ExitOK
}

func (l checkLimits) any() bool {
	return l.maxErrorRate >= 0 || l.max5xxRate >= 0 || l.maxP95 >= 0 || l.maxP99 >= 0
}

// parseLimitDuration parses a Go duration flag; "" means unset (-1).
func parseLimitDuration(s string) (float64, error) {
	if s == "" {
		return -1, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return -1, err
	}
	if d < 0 {
		return -1, fmt.Errorf("negative duration %q", s)
	}
	return d.Seconds(), nil
}

// evaluate prints one verdict line per enforced limit and returns
// whether any breached.
func evaluate(w io.Writer, rep *stats.Report, limits checkLimits) bool {
	breached := checkRow(w, "overall", &rep.Overall, limits)
	if limits.perRoute {
		for i := range rep.Rows {
			r := &rep.Rows[i]
			if r.Count < limits.minRequests {
				continue
			}
			label := fmt.Sprintf("route %s %s", r.Method, r.Route)
			if checkRow(w, label, r, limits) {
				breached = true
			}
		}
	}
	return breached
}

// checkRow evaluates every enforced limit against one row.
func checkRow(w io.Writer, label string, r *stats.Row, l checkLimits) bool {
	breached := false
	verdict := func(name, got, limit string, bad bool) {
		state := "ok"
		if bad {
			state = "BREACH"
			breached = true
		}
		fmt.Fprintf(w, "%-46s %10s  (limit %s)  %s\n", label+" "+name, got, limit, state)
	}
	if l.maxErrorRate >= 0 {
		verdict("error rate", fmtRatePct(r.ErrRate()), fmtRatePct(l.maxErrorRate), r.ErrRate() > l.maxErrorRate)
	}
	if l.max5xxRate >= 0 {
		verdict("5xx rate", fmtRatePct(r.Rate5xx()), fmtRatePct(l.max5xxRate), r.Rate5xx() > l.max5xxRate)
	}
	if l.maxP95 >= 0 {
		verdict("p95", fmtLimitDur(r.P95), fmtLimitDur(l.maxP95), r.P95 > l.maxP95)
	}
	if l.maxP99 >= 0 {
		verdict("p99", fmtLimitDur(r.P99), fmtLimitDur(l.maxP99), r.P99 > l.maxP99)
	}
	return breached
}

func fmtRatePct(p float64) string {
	return fmt.Sprintf("%.1f%%", p)
}

// fmtLimitDur formats seconds for verdict lines; -1 (no samples) reads
// as "n/a" and never breaches.
func fmtLimitDur(sec float64) string {
	if sec < 0 {
		return "n/a"
	}
	return time.Duration(sec * float64(time.Second)).Round(100 * time.Microsecond).String()
}
