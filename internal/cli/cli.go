// Package cli implements the routegauge command-line interface. Run
// takes argv plus two writers and returns an exit code, so the whole
// surface is testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/JaydenCJ/routegauge/internal/parse"
	"github.com/JaydenCJ/routegauge/internal/stats"
	"github.com/JaydenCJ/routegauge/internal/version"
)

// Exit codes. Documented in the README; `check` uses ExitBreach as its
// machine-readable verdict.
const (
	ExitOK      = 0
	ExitBreach  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Stdin is the reader used when a file argument is "-"; tests replace it.
var Stdin io.Reader = os.Stdin

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "report":
		return runView("report", args[1:], stdout, stderr)
	case "endpoints":
		return runView("endpoints", args[1:], stdout, stderr)
	case "errors":
		return runView("errors", args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "routegauge %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "routegauge: unknown flag %q before a subcommand\n\n", args[0])
			usage(stderr)
			return ExitUsage
		}
		// Bare file arguments: treat as `report <files...>`.
		return runView("report", args, stdout, stderr)
	}
}

// analysisFlags are shared by report, endpoints, errors, and check.
type analysisFlags struct {
	logFormat        string
	since            string
	until            string
	method           string
	pathPrefix       string
	clusterThreshold int
	noCluster        bool

	sinceT, untilT time.Time
}

func (a *analysisFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&a.logFormat, "log-format", "auto", "input format: auto, combined, or jsonl")
	fs.StringVar(&a.since, "since", "", "keep requests at/after this time (YYYY-MM-DD or RFC3339)")
	fs.StringVar(&a.until, "until", "", "keep requests before this time (YYYY-MM-DD or RFC3339)")
	fs.StringVar(&a.method, "method", "", "keep only this HTTP method (e.g. GET)")
	fs.StringVar(&a.pathPrefix, "path-prefix", "", "keep only paths under this prefix (e.g. /api)")
	fs.IntVar(&a.clusterThreshold, "cluster-threshold", 0, "distinct literal segments tolerated before collapsing to :param (default 12)")
	fs.BoolVar(&a.noCluster, "no-cluster", false, "disable endpoint clustering; report raw paths")
}

// validate resolves and checks the shared flags.
func (a *analysisFlags) validate() error {
	if !parse.ValidFormat(a.logFormat) {
		return fmt.Errorf("invalid --log-format %q (want auto, combined, or jsonl)", a.logFormat)
	}
	var err error
	if a.sinceT, err = parseWhen(a.since); err != nil {
		return fmt.Errorf("invalid --since: %v", err)
	}
	if a.untilT, err = parseWhen(a.until); err != nil {
		return fmt.Errorf("invalid --until: %v", err)
	}
	a.method = strings.ToUpper(a.method)
	return nil
}

// parseWhen accepts YYYY-MM-DD (UTC midnight) or RFC3339.
func parseWhen(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// runView handles the three read-only subcommands, which differ only in
// output flags and renderer.
func runView(name string, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet(name, stderr)
	var af analysisFlags
	af.register(fs)
	formatHelp := "output format: text or json"
	if name == "report" {
		formatHelp = "output format: text, json, or markdown"
	}
	format := fs.String("format", "text", formatHelp)
	sortKey := fs.String("sort", "requests", "row order: requests, p95, errors, or route")
	top := fs.Int("top", 20, "rows shown in text/markdown output (0 = all)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if err := af.validate(); err != nil {
		fmt.Fprintf(stderr, "routegauge %s: %v\n", name, err)
		return ExitUsage
	}
	if !stats.ValidSortKey(*sortKey) {
		fmt.Fprintf(stderr, "routegauge %s: invalid --sort %q (want one of %s)\n",
			name, *sortKey, strings.Join(stats.SortKeys, ", "))
		return ExitUsage
	}
	switch *format {
	case "text", "json":
	case "markdown":
		if name != "report" {
			fmt.Fprintf(stderr, "routegauge %s: --format markdown is only available for report\n", name)
			return ExitUsage
		}
	default:
		fmt.Fprintf(stderr, "routegauge %s: invalid --format %q\n", name, *format)
		return ExitUsage
	}
	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintf(stderr, "routegauge %s: no input files (pass one or more log files, or - for stdin)\n", name)
		return ExitUsage
	}

	rep, err := analyze(files, &af, *sortKey)
	if err != nil {
		fmt.Fprintf(stderr, "routegauge %s: %v\n", name, err)
		return ExitRuntime
	}
	return renderView(name, *format, rep, *top, stdout, stderr)
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("routegauge "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func usage(w io.Writer) {
	fmt.Fprint(w, `routegauge — API analytics from the access logs you already rotate

usage:
  routegauge report    [flags] <files...>   overview: traffic, errors, percentiles
  routegauge endpoints [flags] <files...>   clustered route patterns with samples
  routegauge errors    [flags] <files...>   error-rate deep dive
  routegauge check     [flags] <files...>   gate on error rate / p95 / p99 (exit 1 on breach)
  routegauge version                        print the version

files may be plain, gzip-rotated (.gz), or - for stdin.

shared flags:
  --log-format auto|combined|jsonl   input dialect (default: auto-detect)
  --since / --until                  time window (YYYY-MM-DD or RFC3339)
  --method GET                       filter by HTTP method
  --path-prefix /api                 filter by path prefix
  --cluster-threshold N              literals tolerated before :param collapse (default 12)
  --no-cluster                       report raw paths, no clustering

report/endpoints/errors flags:
  --format text|json                 output (report also: markdown)
  --sort requests|p95|errors|route   row order (default requests)
  --top N                            rows in text output (default 20, 0 = all)

check flags:
  --max-error-rate PCT               fail when 4xx+5xx share exceeds PCT
  --max-5xx-rate PCT                 fail when 5xx share exceeds PCT
  --max-p95 DUR / --max-p99 DUR      fail when latency exceeds DUR (e.g. 250ms, 1.5s)
  --per-route                        also enforce limits per route
  --min-requests N                   per-route minimum sample size (default 10)

exit codes: 0 ok · 1 check breach · 2 usage error · 3 runtime error
`)
}
