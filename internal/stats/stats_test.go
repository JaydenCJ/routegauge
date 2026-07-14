// Tests for aggregation: percentile math, error accounting, group
// merging under final routes, and deterministic ordering.
package stats

import (
	"math"
	"testing"
	"time"

	"github.com/JaydenCJ/routegauge/internal/parse"
)

// ev builds a minimal event for aggregation tests.
func ev(method, path string, status int, latency float64) parse.Event {
	return parse.Event{Method: method, Path: path, Status: status, Latency: latency, Bytes: -1}
}

// identity is the routeOf function when clustering is not under test.
func identity(p string) string { return p }

func TestPercentileNearestRank(t *testing.T) {
	// 1..100 sorted: nearest-rank pXX is exactly XX.
	var sorted []float64
	for i := 1; i <= 100; i++ {
		sorted = append(sorted, float64(i))
	}
	for q, want := range map[float64]float64{0.50: 50, 0.90: 90, 0.95: 95, 0.99: 99} {
		if got := Percentile(sorted, q); got != want {
			t.Errorf("Percentile(1..100, %v) = %v, want %v", q, got, want)
		}
	}
}

func TestPercentileSmallAndEmptySamples(t *testing.T) {
	if got := Percentile([]float64{7}, 0.99); got != 7 {
		t.Fatalf("single sample p99 = %v, want 7", got)
	}
	// n=4, p50 → rank ceil(2)=2 → second value.
	if got := Percentile([]float64{1, 2, 3, 4}, 0.50); got != 2 {
		t.Fatalf("p50 of 4 = %v, want 2", got)
	}
	// p99 of 4 → rank ceil(3.96)=4 → last value.
	if got := Percentile([]float64{1, 2, 3, 4}, 0.99); got != 4 {
		t.Fatalf("p99 of 4 = %v, want 4", got)
	}
	if got := Percentile(nil, 0.95); got != -1 {
		t.Fatalf("empty percentile = %v, want -1 sentinel", got)
	}
}

func TestRowMetricsComputed(t *testing.T) {
	c := NewCollector()
	for i, lat := range []float64{0.010, 0.020, 0.030, 0.040, 0.050} {
		status := 200
		if i == 4 {
			status = 500
		}
		c.Add(ev("GET", "/users/:id", status, lat), "/users/:id")
	}
	rep := c.Finalize(identity, "requests")
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	r := rep.Rows[0]
	if r.Count != 5 || r.C5xx != 1 || r.LatencyN != 5 {
		t.Fatalf("counts wrong: %+v", r)
	}
	if r.P50 != 0.030 || r.P95 != 0.050 || r.Max != 0.050 {
		t.Fatalf("percentiles wrong: p50=%v p95=%v max=%v", r.P50, r.P95, r.Max)
	}
	if math.Abs(r.Avg-0.030) > 1e-9 {
		t.Fatalf("avg = %v, want 0.030", r.Avg)
	}
	if r.ErrRate() != 20 || r.Rate5xx() != 20 {
		t.Fatalf("error rates wrong: %v / %v", r.ErrRate(), r.Rate5xx())
	}
}

func TestRouteMergingCombinesGroups(t *testing.T) {
	// Two normalized paths that the clusterer maps to one route must
	// merge their samples before percentiles are computed.
	c := NewCollector()
	c.Add(ev("GET", "/docs/alpha", 200, 0.100), "/docs/alpha")
	c.Add(ev("GET", "/docs/beta", 200, 0.300), "/docs/beta")
	routeOf := func(string) string { return "/docs/:param" }
	rep := c.Finalize(routeOf, "requests")
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 merged route", len(rep.Rows))
	}
	r := rep.Rows[0]
	if r.Count != 2 || r.DistinctPaths != 2 || r.Max != 0.300 {
		t.Fatalf("merge wrong: %+v", r)
	}
}

func TestMethodsStaySeparateButSharePaths(t *testing.T) {
	// GET and POST on one path are distinct rows, yet NormPaths dedupes
	// across methods so the clusterer sees each path exactly once.
	c := NewCollector()
	c.Add(ev("GET", "/orders", 200, -1), "/orders")
	c.Add(ev("POST", "/orders", 201, -1), "/orders")
	c.Add(ev("GET", "/users/:id", 200, -1), "/users/:id")
	rep := c.Finalize(identity, "requests")
	if len(rep.Rows) != 3 {
		t.Fatalf("rows = %d, want GET and POST kept apart", len(rep.Rows))
	}
	if got := c.NormPaths(); len(got) != 2 {
		t.Fatalf("NormPaths = %v, want 2 method-independent paths", got)
	}
}

func TestStatusClassesCounted(t *testing.T) {
	c := NewCollector()
	for _, status := range []int{200, 204, 301, 404, 404, 500} {
		c.Add(ev("GET", "/x", status, -1), "/x")
	}
	rep := c.Finalize(identity, "requests")
	want := map[string]int{"2xx": 2, "3xx": 1, "4xx": 2, "5xx": 1}
	for class, n := range want {
		if rep.Class[class] != n {
			t.Errorf("class %s = %d, want %d", class, rep.Class[class], n)
		}
	}
}

func TestTimeWindowTracked(t *testing.T) {
	c := NewCollector()
	t1 := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	e := ev("GET", "/x", 200, -1)
	e.Time = t2
	c.Add(e, "/x")
	e.Time = t1
	c.Add(e, "/x")
	e.Time = time.Time{} // unknown timestamps must not clobber the window
	c.Add(e, "/x")
	rep := c.Finalize(identity, "requests")
	if !rep.First.Equal(t1) || !rep.Last.Equal(t2) {
		t.Fatalf("window = %v → %v", rep.First, rep.Last)
	}
}

func TestNoLatencyRowsUseSentinels(t *testing.T) {
	c := NewCollector()
	c.Add(ev("GET", "/x", 200, -1), "/x")
	rep := c.Finalize(identity, "requests")
	r := rep.Rows[0]
	if r.P95 != -1 || r.Avg != -1 || r.LatencyN != 0 || rep.HasLatency {
		t.Fatalf("latency sentinels wrong: %+v hasLatency=%v", r, rep.HasLatency)
	}
}

func TestOverallAggregatesEverything(t *testing.T) {
	c := NewCollector()
	c.Add(ev("GET", "/a", 200, 0.010), "/a")
	c.Add(ev("GET", "/b", 500, 0.090), "/b")
	rep := c.Finalize(identity, "requests")
	if rep.Overall.Count != 2 || rep.Overall.C5xx != 1 {
		t.Fatalf("overall wrong: %+v", rep.Overall)
	}
	if rep.Overall.Max != 0.090 {
		t.Fatalf("overall max = %v", rep.Overall.Max)
	}
}

func TestSortByRequestsDefault(t *testing.T) {
	c := NewCollector()
	c.Add(ev("GET", "/rare", 200, -1), "/rare")
	for i := 0; i < 3; i++ {
		c.Add(ev("GET", "/hot", 200, -1), "/hot")
	}
	rep := c.Finalize(identity, "requests")
	if rep.Rows[0].Route != "/hot" {
		t.Fatalf("first row = %q, want /hot", rep.Rows[0].Route)
	}
}

func TestSortKeyOrdering(t *testing.T) {
	// p95: slowest first, even with fewer requests.
	c := NewCollector()
	c.Add(ev("GET", "/fast", 200, 0.005), "/fast")
	c.Add(ev("GET", "/slow", 200, 2.5), "/slow")
	if rep := c.Finalize(identity, "p95"); rep.Rows[0].Route != "/slow" {
		t.Fatalf("p95 sort: first row = %q, want /slow", rep.Rows[0].Route)
	}
	// errors: 5xx outranks more-numerous 4xx.
	c = NewCollector()
	c.Add(ev("GET", "/notfound", 404, -1), "/notfound")
	c.Add(ev("GET", "/notfound", 404, -1), "/notfound")
	c.Add(ev("GET", "/broken", 500, -1), "/broken")
	if rep := c.Finalize(identity, "errors"); rep.Rows[0].Route != "/broken" {
		t.Fatalf("errors sort: first row = %q, want /broken", rep.Rows[0].Route)
	}
	// route: lexicographic.
	c = NewCollector()
	for _, p := range []string{"/zebra", "/alpha", "/mid"} {
		c.Add(ev("GET", p, 200, -1), p)
	}
	rep := c.Finalize(identity, "route")
	want := []string{"/alpha", "/mid", "/zebra"}
	for i := range want {
		if rep.Rows[i].Route != want[i] {
			t.Fatalf("route sort: row %d = %q, want %q", i, rep.Rows[i].Route, want[i])
		}
	}
}

func TestSortIsDeterministicOnTies(t *testing.T) {
	// Equal request counts must fall back to route order, so repeated
	// runs emit byte-identical reports.
	for run := 0; run < 5; run++ {
		c := NewCollector()
		for _, p := range []string{"/c", "/a", "/b"} {
			c.Add(ev("GET", p, 200, -1), p)
		}
		rep := c.Finalize(identity, "requests")
		if rep.Rows[0].Route != "/a" || rep.Rows[2].Route != "/c" {
			t.Fatalf("tie-break unstable: %q first", rep.Rows[0].Route)
		}
	}
}

func TestSamplePathsCappedAndSorted(t *testing.T) {
	c := NewCollector()
	for _, p := range []string{"/u/9", "/u/1", "/u/5", "/u/3", "/u/7"} {
		c.Add(ev("GET", p, 200, -1), "/u/:id")
	}
	rep := c.Finalize(identity, "requests")
	r := rep.Rows[0]
	if r.DistinctPaths != 5 {
		t.Fatalf("distinct = %d, want 5", r.DistinctPaths)
	}
	if len(r.Samples) != MaxSamples {
		t.Fatalf("samples = %v, want %d entries", r.Samples, MaxSamples)
	}
	for i := 1; i < len(r.Samples); i++ {
		if r.Samples[i-1] > r.Samples[i] {
			t.Fatalf("samples not sorted: %v", r.Samples)
		}
	}
}

func TestSkippedCounter(t *testing.T) {
	c := NewCollector()
	c.Skip()
	c.Skip()
	if c.Skipped() != 2 {
		t.Fatalf("skipped = %d", c.Skipped())
	}
	rep := c.Finalize(identity, "requests")
	if rep.Skipped != 2 {
		t.Fatalf("report skipped = %d", rep.Skipped)
	}
}
