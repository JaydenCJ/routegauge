// Tests for the three renderers. Text assertions check structure and
// key figures rather than every byte, so cosmetic tweaks don't churn
// the suite; JSON assertions decode and verify the document.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/routegauge/internal/parse"
	"github.com/JaydenCJ/routegauge/internal/stats"
)

// demoReport builds a small two-route report with one 5xx.
func demoReport() *stats.Report {
	c := stats.NewCollector()
	base := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	add := func(method, path, norm string, status int, lat float64, offset int) {
		c.Add(parse.Event{
			Time: base.Add(time.Duration(offset) * time.Second), Method: method,
			Path: path, Status: status, Latency: lat, Bytes: 100,
		}, norm)
	}
	add("GET", "/users/1", "/users/:id", 200, 0.010, 0)
	add("GET", "/users/2", "/users/:id", 200, 0.020, 1)
	add("GET", "/users/3", "/users/:id", 404, 0.005, 2)
	add("POST", "/orders", "/orders", 500, 1.500, 3)
	return c.Finalize(func(p string) string { return p }, "requests")
}

func TestTextReportContainsKeyFigures(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, demoReport(), 20)
	out := buf.String()
	for _, want := range []string{
		"routegauge report — 4 requests, 2 routes",
		"window: 2026-07-06 09:00:00 UTC → 2026-07-06 09:00:03 UTC",
		"/users/:id",
		"█", // status gauge present
		"overall p95 1.50s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text report missing %q in:\n%s", want, out)
		}
	}
}

func TestTextReportTopLimitsRows(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, demoReport(), 1)
	out := buf.String()
	if !strings.Contains(out, "2 routes total (top 1 shown)") {
		t.Fatalf("top annotation missing:\n%s", out)
	}
	if strings.Contains(out, "POST") {
		t.Fatalf("second row should be cut:\n%s", out)
	}
}

func TestTextReportNoLatencyNote(t *testing.T) {
	c := stats.NewCollector()
	c.Add(parse.Event{Method: "GET", Path: "/x", Status: 200, Latency: -1, Bytes: -1}, "/x")
	rep := c.Finalize(func(p string) string { return p }, "requests")
	var buf bytes.Buffer
	Text(&buf, rep, 20)
	out := buf.String()
	if !strings.Contains(out, "no latency field") {
		t.Fatalf("missing latency hint:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Fatalf("percentile columns should show em dashes:\n%s", out)
	}
}

func TestEndpointsShowsSamplesAndDistincts(t *testing.T) {
	var buf bytes.Buffer
	Endpoints(&buf, demoReport(), 0)
	out := buf.String()
	for _, want := range []string{
		"2 route patterns",
		"3 requests, 3 distinct paths",
		"e.g. /users/1, /users/2, /users/3",
		"1 request, 1 distinct path", // singular grammar
	} {
		if !strings.Contains(out, want) {
			t.Errorf("endpoints missing %q in:\n%s", want, out)
		}
	}
}

func TestErrorsViewHistogramAndRows(t *testing.T) {
	var buf bytes.Buffer
	Errors(&buf, demoReport(), 20)
	out := buf.String()
	for _, want := range []string{
		"2 error responses, 50.0% of 4 requests",
		"status codes",
		"500", "404",
		"top status",
		"500×1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("errors view missing %q in:\n%s", want, out)
		}
	}
	// 5xx routes come first regardless of counts.
	if strings.Index(out, "/orders") > strings.Index(out, "/users/:id") {
		t.Fatalf("5xx route not sorted first:\n%s", out)
	}
}

func TestErrorsViewCleanWindow(t *testing.T) {
	c := stats.NewCollector()
	c.Add(parse.Event{Method: "GET", Path: "/x", Status: 200, Latency: -1, Bytes: -1}, "/x")
	rep := c.Finalize(func(p string) string { return p }, "requests")
	var buf bytes.Buffer
	Errors(&buf, rep, 20)
	if !strings.Contains(buf.String(), "no 4xx or 5xx responses") {
		t.Fatalf("clean window message missing:\n%s", buf.String())
	}
}

func TestJSONDocumentDecodesWithEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, demoReport()); err != nil {
		t.Fatalf("JSON render failed: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc["tool"] != "routegauge" || doc["schema_version"] != float64(SchemaVersion) {
		t.Fatalf("envelope wrong: %v %v", doc["tool"], doc["schema_version"])
	}
	if doc["requests"] != float64(4) {
		t.Fatalf("requests = %v", doc["requests"])
	}
	routes := doc["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("routes = %d", len(routes))
	}
	first := routes[0].(map[string]any)
	if first["route"] != "/users/:id" {
		t.Fatalf("first route = %v", first["route"])
	}
	lat := first["latency"].(map[string]any)
	if lat["p95_ms"] != float64(20) {
		t.Fatalf("p95_ms = %v, want 20", lat["p95_ms"])
	}
}

func TestJSONNullsForAbsentLatencyAndWindow(t *testing.T) {
	// Logs without $request_time or timestamps must yield explicit
	// nulls, never zeros a script would mistake for measurements.
	c := stats.NewCollector()
	c.Add(parse.Event{Method: "GET", Path: "/x", Status: 200, Latency: -1, Bytes: -1}, "/x")
	rep := c.Finalize(func(p string) string { return p }, "requests")
	var buf bytes.Buffer
	if err := JSON(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"latency": null`) {
		t.Fatalf("latency should be null:\n%s", out)
	}
	if !strings.Contains(out, `"window": null`) {
		t.Fatalf("window should be null:\n%s", out)
	}
}

func TestMarkdownTables(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, demoReport(), 20)
	out := buf.String()
	for _, want := range []string{
		"# routegauge report",
		"| Method | Endpoint | Requests | Err% | p50 | p95 | p99 | Max |",
		"| GET | `/users/:id` |",
		"| Class | Requests | Share |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q in:\n%s", want, out)
		}
	}
}

func TestFormattingHelpers(t *testing.T) {
	durCases := map[float64]string{
		-1:     "—",
		0.0004: "0.40ms",
		0.0042: "4.2ms",
		0.042:  "42ms",
		0.999:  "999ms",
		1.204:  "1.20s",
		75.5:   "75.5s",
	}
	for in, want := range durCases {
		if got := fmtDur(in); got != want {
			t.Errorf("fmtDur(%v) = %q, want %q", in, got, want)
		}
	}
	countCases := map[int]string{0: "0", 999: "999", 1000: "1,000", 12847: "12,847", 1234567: "1,234,567"}
	for in, want := range countCases {
		if got := fmtCount(in); got != want {
			t.Errorf("fmtCount(%d) = %q, want %q", in, got, want)
		}
	}
	if g := gauge(0.5, 10); g != "█████░░░░░" {
		t.Errorf("gauge(0.5) = %q", g)
	}
	if g := gauge(0, 4); g != "░░░░" {
		t.Errorf("gauge(0) = %q", g)
	}
	if g := gauge(1.5, 4); g != "████" { // clamped
		t.Errorf("gauge(1.5) = %q", g)
	}
}
