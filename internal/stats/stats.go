// Package stats aggregates parsed events into per-route metrics:
// request counts, status-class breakdowns, error rates, and exact
// latency percentiles. Aggregation is two-phase to match the clusterer:
// events are grouped under their heuristic-normalized path first, and
// groups are merged into final routes once the cardinality pass has
// decided the route patterns.
package stats

import (
	"math"
	"sort"
	"time"

	"github.com/JaydenCJ/routegauge/internal/parse"
)

// MaxSamples is how many example raw paths each route retains.
const MaxSamples = 3

// Row is the finished metric set for one method+route pair. Percentile
// fields are -1 when the route has no latency samples.
type Row struct {
	Method        string
	Route         string
	Count         int
	C4xx          int
	C5xx          int
	Statuses      map[int]int
	DistinctPaths int
	Samples       []string
	Bytes         int64
	LatencyN      int
	P50, P90, P95 float64
	P99, Avg, Max float64
}

// ErrRate is the 4xx+5xx share of requests, as a percentage.
func (r *Row) ErrRate() float64 {
	if r.Count == 0 {
		return 0
	}
	return 100 * float64(r.C4xx+r.C5xx) / float64(r.Count)
}

// Rate5xx is the 5xx share of requests, as a percentage.
func (r *Row) Rate5xx() float64 {
	if r.Count == 0 {
		return 0
	}
	return 100 * float64(r.C5xx) / float64(r.Count)
}

// Report is everything the renderers need.
type Report struct {
	Requests    int
	Skipped     int
	First, Last time.Time
	Class       map[string]int // "1xx" … "5xx"
	Rows        []Row          // sorted per the requested key
	Overall     Row            // all routes merged; Method/Route empty
	HasLatency  bool
}

// group accumulates one (method, normalized path) bucket.
type group struct {
	count    int
	c4xx     int
	c5xx     int
	statuses map[int]int
	lat      []float64
	bytes    int64
	raw      map[string]struct{}
	samples  []string
}

type groupKey struct {
	method   string
	normPath string
}

// Collector ingests events keyed by their normalized path.
type Collector struct {
	groups      map[groupKey]*group
	skipped     int
	first, last time.Time
	class       map[string]int
	requests    int
}

// NewCollector returns an empty Collector.
func NewCollector() *Collector {
	return &Collector{
		groups: map[groupKey]*group{},
		class:  map[string]int{},
	}
}

// Add records one event under its normalized path.
func (c *Collector) Add(ev parse.Event, normPath string) {
	c.requests++
	if !ev.Time.IsZero() {
		if c.first.IsZero() || ev.Time.Before(c.first) {
			c.first = ev.Time
		}
		if c.last.IsZero() || ev.Time.After(c.last) {
			c.last = ev.Time
		}
	}
	c.class[classOf(ev.Status)]++

	k := groupKey{method: ev.Method, normPath: normPath}
	g := c.groups[k]
	if g == nil {
		g = &group{statuses: map[int]int{}, raw: map[string]struct{}{}}
		c.groups[k] = g
	}
	g.count++
	g.statuses[ev.Status]++
	switch {
	case ev.Status >= 500 && ev.Status <= 599:
		g.c5xx++
	case ev.Status >= 400 && ev.Status <= 499:
		g.c4xx++
	}
	if ev.HasLatency() {
		g.lat = append(g.lat, ev.Latency)
	}
	if ev.Bytes > 0 {
		g.bytes += ev.Bytes
	}
	if _, seen := g.raw[ev.Path]; !seen {
		g.raw[ev.Path] = struct{}{}
		if len(g.samples) < MaxSamples {
			g.samples = append(g.samples, ev.Path)
		}
	}
}

// Skip counts one unparseable line.
func (c *Collector) Skip() { c.skipped++ }

// Skipped returns the running count of unparseable lines.
func (c *Collector) Skipped() int { return c.skipped }

// Requests returns the running count of kept events.
func (c *Collector) Requests() int { return c.requests }

// NormPaths returns every distinct (method-independent) normalized path
// seen so far, for feeding the clusterer exactly once each.
func (c *Collector) NormPaths() []string {
	set := map[string]struct{}{}
	for k := range c.groups {
		set[k.normPath] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Finalize merges groups into their final routes and computes all
// derived metrics. routeOf maps a normalized path to its route; sortKey
// is one of "requests", "p95", "errors", "route".
func (c *Collector) Finalize(routeOf func(string) string, sortKey string) *Report {
	merged := map[groupKey]*group{}
	for k, g := range c.groups {
		fk := groupKey{method: k.method, normPath: routeOf(k.normPath)}
		if dst := merged[fk]; dst != nil {
			mergeGroup(dst, g)
		} else {
			merged[fk] = g
		}
	}

	rep := &Report{
		Requests: c.requests,
		Skipped:  c.skipped,
		First:    c.first,
		Last:     c.last,
		Class:    c.class,
	}
	overall := &group{statuses: map[int]int{}, raw: map[string]struct{}{}}
	for k, g := range merged {
		row := finishRow(k.method, k.normPath, g)
		rep.Rows = append(rep.Rows, row)
		if row.LatencyN > 0 {
			rep.HasLatency = true
		}
		mergeGroup(overall, g)
	}
	rep.Overall = finishRow("", "", overall)
	sortRows(rep.Rows, sortKey)
	return rep
}

func mergeGroup(dst, src *group) {
	dst.count += src.count
	dst.c4xx += src.c4xx
	dst.c5xx += src.c5xx
	dst.bytes += src.bytes
	dst.lat = append(dst.lat, src.lat...)
	for s, n := range src.statuses {
		dst.statuses[s] += n
	}
	for p := range src.raw {
		if _, seen := dst.raw[p]; !seen {
			dst.raw[p] = struct{}{}
			if len(dst.samples) < MaxSamples {
				dst.samples = append(dst.samples, p)
			}
		}
	}
}

func finishRow(method, route string, g *group) Row {
	row := Row{
		Method:        method,
		Route:         route,
		Count:         g.count,
		C4xx:          g.c4xx,
		C5xx:          g.c5xx,
		Statuses:      g.statuses,
		DistinctPaths: len(g.raw),
		Samples:       append([]string(nil), g.samples...),
		Bytes:         g.bytes,
		LatencyN:      len(g.lat),
		P50:           -1, P90: -1, P95: -1, P99: -1, Avg: -1, Max: -1,
	}
	sort.Strings(row.Samples)
	if len(g.lat) == 0 {
		return row
	}
	sorted := append([]float64(nil), g.lat...)
	sort.Float64s(sorted)
	row.P50 = Percentile(sorted, 0.50)
	row.P90 = Percentile(sorted, 0.90)
	row.P95 = Percentile(sorted, 0.95)
	row.P99 = Percentile(sorted, 0.99)
	row.Max = sorted[len(sorted)-1]
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	row.Avg = sum / float64(len(sorted))
	return row
}

// Percentile returns the nearest-rank percentile of an ascending-sorted
// slice: the smallest sample such that at least q of the distribution
// is at or below it. Exact and deterministic; -1 on empty input.
func Percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return -1
	}
	rank := int(math.Ceil(q * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// SortKeys lists the accepted --sort values.
var SortKeys = []string{"requests", "p95", "errors", "route"}

// ValidSortKey reports whether s is an accepted --sort value.
func ValidSortKey(s string) bool {
	for _, k := range SortKeys {
		if s == k {
			return true
		}
	}
	return false
}

// sortRows orders rows by the requested key, always breaking ties by
// route then method so output is byte-stable across runs.
func sortRows(rows []Row, key string) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := &rows[i], &rows[j]
		switch key {
		case "p95":
			if a.P95 != b.P95 {
				return a.P95 > b.P95
			}
		case "errors":
			ae, be := a.C4xx+a.C5xx, b.C4xx+b.C5xx
			if a.C5xx != b.C5xx {
				return a.C5xx > b.C5xx
			}
			if ae != be {
				return ae > be
			}
		case "route":
			// fall through to the shared tiebreak
		default: // "requests"
			if a.Count != b.Count {
				return a.Count > b.Count
			}
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		return a.Method < b.Method
	})
}

func classOf(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
