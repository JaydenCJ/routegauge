// Tests for JSON-lines parsing: field aliases, latency units, epoch
// scaling, and rejection of records that cannot be analyzed.
package parse

import (
	"math"
	"testing"
	"time"
)

func mustParseJSONL(t *testing.T, line string) Event {
	t.Helper()
	ev, err := ParseJSONL(line)
	if err != nil {
		t.Fatalf("ParseJSONL(%q) failed: %v", line, err)
	}
	return ev
}

func TestJSONLNginxStyleRecord(t *testing.T) {
	ev := mustParseJSONL(t,
		`{"time":"2026-07-13T10:00:00Z","method":"get","path":"/users/42","status":200,"request_time":0.031,"body_bytes_sent":512,"remote_addr":"127.0.0.1"}`)
	if ev.Method != "GET" || ev.Path != "/users/42" || ev.Status != 200 {
		t.Fatalf("core fields wrong: %+v", ev)
	}
	if ev.Latency != 0.031 || ev.Bytes != 512 || ev.Remote != "127.0.0.1" {
		t.Fatalf("aux fields wrong: %+v", ev)
	}
	if !ev.Time.Equal(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("time wrong: %v", ev.Time)
	}
}

func TestJSONLLatencyUnitTable(t *testing.T) {
	// Unit-suffixed names scale to seconds; string values that parse
	// as Go durations ("12.5ms" from time.Duration.String()) win, and
	// stringified numbers still honor the field's implied unit.
	cases := map[string]float64{
		`{"path":"/x","status":200,"duration_ms":250}`:      0.250,
		`{"path":"/x","status":200,"latency_us":1500}`:      0.0015,
		`{"path":"/x","status":200,"duration_ns":2000000}`:  0.002,
		`{"path":"/x","status":200,"duration":"12.5ms"}`:    0.0125,
		`{"path":"/x","status":200,"request_time":"0.75"}`:  0.75,
		`{"path":"/x","status":200,"response_time":0.033}`:  0.033,
		`{"path":"/x","status":200,"time_taken_ms":"88.5"}`: 0.0885,
	}
	for line, want := range cases {
		ev := mustParseJSONL(t, line)
		if math.Abs(ev.Latency-want) > 1e-12 {
			t.Errorf("%s: latency = %v, want %v", line, ev.Latency, want)
		}
	}
}

func TestJSONLNumericStringStatus(t *testing.T) {
	// Logstash pipelines frequently stringify numbers.
	ev := mustParseJSONL(t, `{"path":"/x","status":"503"}`)
	if ev.Status != 503 {
		t.Fatalf("stringified status not handled: %+v", ev)
	}
}

func TestJSONLPathResolution(t *testing.T) {
	// The request-line fallback fills gaps; explicit path fields beat
	// it; full URLs are reduced to their path.
	cases := []struct {
		line   string
		method string
		path   string
	}{
		{`{"request":"POST /api/orders?id=7 HTTP/1.1","status":201}`, "POST", "/api/orders"},
		{`{"uri":"/from-uri","request":"GET /from-request HTTP/1.1","status":200}`, "GET", "/from-uri"},
		{`{"url":"https://api.example.test/v2/items?page=3","status":200}`, "-", "/v2/items"},
	}
	for _, c := range cases {
		ev := mustParseJSONL(t, c.line)
		if ev.Method != c.method || ev.Path != c.path {
			t.Errorf("%s: got %s %s, want %s %s", c.line, ev.Method, ev.Path, c.method, c.path)
		}
	}
}

func TestJSONLTimestampFormats(t *testing.T) {
	// Numeric epochs are scaled by magnitude; string epochs parse too.
	want := time.Date(2026, 7, 3, 10, 40, 0, 0, time.UTC)
	for _, line := range []string{
		`{"path":"/x","status":200,"ts":1783075200}`,
		`{"path":"/x","status":200,"timestamp":1783075200000}`,
		`{"path":"/x","status":200,"time":"1783075200"}`,
	} {
		ev := mustParseJSONL(t, line)
		if !ev.Time.Equal(want) {
			t.Errorf("%s: time = %v, want %v", line, ev.Time, want)
		}
	}
}

func TestJSONLMissingFieldDefaults(t *testing.T) {
	ev := mustParseJSONL(t, `{"path":"/x","status":200}`)
	if ev.Method != "-" {
		t.Fatalf("method = %q, want -", ev.Method)
	}
	if ev.HasLatency() || ev.Bytes != -1 || !ev.Time.IsZero() {
		t.Fatalf("sentinels wrong: %+v", ev)
	}
}

func TestJSONLRejectsUnusableRecords(t *testing.T) {
	// No path source, no status, or broken JSON: none can be analyzed.
	for _, line := range []string{
		`{"status":200,"method":"GET"}`,
		`{"path":"/x"}`,
		`{"path": /x}`,
	} {
		if _, err := ParseJSONL(line); err == nil {
			t.Errorf("ParseJSONL(%q) should fail", line)
		}
	}
}
