// The analysis pipeline: stream lines from every input, parse, filter,
// aggregate under normalized paths, then run the clustering pass and
// merge into final routes.
package cli

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/routegauge/internal/cluster"
	"github.com/JaydenCJ/routegauge/internal/logread"
	"github.com/JaydenCJ/routegauge/internal/parse"
	"github.com/JaydenCJ/routegauge/internal/render"
	"github.com/JaydenCJ/routegauge/internal/stats"
)

// analyze reads every input file and produces the finished report.
func analyze(files []string, af *analysisFlags, sortKey string) (*stats.Report, error) {
	clu := cluster.New(cluster.Options{
		Threshold: af.clusterThreshold,
		Disabled:  af.noCluster,
	})
	col := stats.NewCollector()
	format := parse.Format(af.logFormat)

	for _, name := range files {
		fileFormat := format
		err := logread.EachLine(name, Stdin, func(line string) {
			if fileFormat == parse.FormatAuto {
				fileFormat = parse.Detect(line)
			}
			ev, err := parse.Line(line, fileFormat)
			if err != nil {
				col.Skip()
				return
			}
			if !keep(ev, af) {
				return
			}
			col.Add(ev, clu.Normalize(ev.Path))
		})
		if err != nil {
			return nil, err
		}
	}
	if col.Skipped() > 0 && col.Requests() == 0 {
		// Every single line failed to parse: almost certainly a wrong
		// --log-format rather than a dirty log; fail loudly.
		return nil, fmt.Errorf("all %d lines were unparseable — check --log-format", col.Skipped())
	}

	for _, p := range col.NormPaths() {
		clu.Observe(p)
	}
	clu.Finalize()
	return col.Finalize(clu.Route, sortKey), nil
}

// keep applies the --since/--until/--method/--path-prefix filters.
func keep(ev parse.Event, af *analysisFlags) bool {
	if af.method != "" && ev.Method != af.method {
		return false
	}
	if af.pathPrefix != "" && !hasPathPrefix(ev.Path, af.pathPrefix) {
		return false
	}
	if !af.sinceT.IsZero() && (ev.Time.IsZero() || ev.Time.Before(af.sinceT)) {
		return false
	}
	if !af.untilT.IsZero() && (ev.Time.IsZero() || !ev.Time.Before(af.untilT)) {
		return false
	}
	return true
}

// hasPathPrefix matches whole segments: /api matches /api and /api/x
// but not /apiary.
func hasPathPrefix(path, prefix string) bool {
	if len(path) < len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

// renderView writes the requested view in the requested format.
func renderView(name, format string, rep *stats.Report, top int, stdout, stderr io.Writer) int {
	switch format {
	case "json":
		if err := render.JSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "routegauge %s: %v\n", name, err)
			return ExitRuntime
		}
	case "markdown":
		render.Markdown(stdout, rep, top)
	default:
		switch name {
		case "endpoints":
			render.Endpoints(stdout, rep, top)
		case "errors":
			render.Errors(stdout, rep, top)
		default:
			render.Text(stdout, rep, top)
		}
	}
	return ExitOK
}
