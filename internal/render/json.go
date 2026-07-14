// Stable JSON output. The envelope is versioned (schema_version) so
// downstream scripts can pin against it; all latencies are emitted in
// milliseconds rounded to 3 decimals, and route order matches the
// requested sort so diffs stay clean.
package render

import (
	"encoding/json"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/JaydenCJ/routegauge/internal/stats"
	"github.com/JaydenCJ/routegauge/internal/version"
)

// SchemaVersion identifies the JSON document layout.
const SchemaVersion = 1

type jsonDoc struct {
	Tool          string         `json:"tool"`
	SchemaVersion int            `json:"schema_version"`
	Version       string         `json:"version"`
	Requests      int            `json:"requests"`
	SkippedLines  int            `json:"skipped_lines"`
	Window        *jsonWindow    `json:"window"`
	StatusClasses map[string]int `json:"status_classes"`
	Overall       jsonRoute      `json:"overall"`
	Routes        []jsonRoute    `json:"routes"`
}

type jsonWindow struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type jsonRoute struct {
	Method        string         `json:"method,omitempty"`
	Route         string         `json:"route,omitempty"`
	Requests      int            `json:"requests"`
	DistinctPaths int            `json:"distinct_paths"`
	SamplePaths   []string       `json:"sample_paths,omitempty"`
	Status4xx     int            `json:"status_4xx"`
	Status5xx     int            `json:"status_5xx"`
	ErrorRatePct  float64        `json:"error_rate_pct"`
	Rate5xxPct    float64        `json:"rate_5xx_pct"`
	Statuses      map[string]int `json:"statuses"`
	BytesTotal    int64          `json:"bytes_total"`
	Latency       *jsonLatency   `json:"latency"`
}

type jsonLatency struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P90Ms   float64 `json:"p90_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	AvgMs   float64 `json:"avg_ms"`
	MaxMs   float64 `json:"max_ms"`
}

// JSON writes the machine-readable report. Unlike the text views it
// always includes every route, so --top does not apply.
func JSON(w io.Writer, rep *stats.Report) error {
	doc := jsonDoc{
		Tool:          "routegauge",
		SchemaVersion: SchemaVersion,
		Version:       version.Version,
		Requests:      rep.Requests,
		SkippedLines:  rep.Skipped,
		StatusClasses: rep.Class,
		Overall:       toJSONRoute(&rep.Overall),
		Routes:        make([]jsonRoute, 0, len(rep.Rows)),
	}
	if !rep.First.IsZero() {
		doc.Window = &jsonWindow{
			First: rep.First.UTC().Format(time.RFC3339),
			Last:  rep.Last.UTC().Format(time.RFC3339),
		}
	}
	for i := range rep.Rows {
		doc.Routes = append(doc.Routes, toJSONRoute(&rep.Rows[i]))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func toJSONRoute(r *stats.Row) jsonRoute {
	out := jsonRoute{
		Method:        r.Method,
		Route:         r.Route,
		Requests:      r.Count,
		DistinctPaths: r.DistinctPaths,
		SamplePaths:   r.Samples,
		Status4xx:     r.C4xx,
		Status5xx:     r.C5xx,
		ErrorRatePct:  round1(r.ErrRate()),
		Rate5xxPct:    round1(r.Rate5xx()),
		Statuses:      map[string]int{},
		BytesTotal:    r.Bytes,
	}
	for code, n := range r.Statuses {
		out.Statuses[strconv.Itoa(code)] = n
	}
	if r.LatencyN > 0 {
		out.Latency = &jsonLatency{
			Samples: r.LatencyN,
			P50Ms:   roundMs(r.P50),
			P90Ms:   roundMs(r.P90),
			P95Ms:   roundMs(r.P95),
			P99Ms:   roundMs(r.P99),
			AvgMs:   roundMs(r.Avg),
			MaxMs:   roundMs(r.Max),
		}
	}
	return out
}

// roundMs converts seconds to milliseconds at microsecond precision.
func roundMs(sec float64) float64 {
	return math.Round(sec*1e6) / 1e3
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
