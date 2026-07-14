// Tests for combined/common-format parsing. Lines are lifted from real
// nginx and Apache configurations, including the quoting and
// request-time edge cases that break naive regex parsers.
package parse

import (
	"testing"
	"time"
)

func mustParseCombined(t *testing.T, line string) Event {
	t.Helper()
	ev, err := ParseCombined(line)
	if err != nil {
		t.Fatalf("ParseCombined(%q) failed: %v", line, err)
	}
	return ev
}

func TestCombinedBasicLine(t *testing.T) {
	ev := mustParseCombined(t,
		`127.0.0.1 - alice [13/Jul/2026:10:00:00 +0000] "GET /users/123 HTTP/1.1" 200 512 "-" "curl/8.5.0"`)
	if ev.Method != "GET" || ev.Path != "/users/123" {
		t.Fatalf("request line parsed as %s %s", ev.Method, ev.Path)
	}
	if ev.Status != 200 || ev.Bytes != 512 || ev.Remote != "127.0.0.1" {
		t.Fatalf("status/bytes/remote wrong: %+v", ev)
	}
	want := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	if !ev.Time.Equal(want) {
		t.Fatalf("time = %v, want %v", ev.Time, want)
	}
}

func TestCombinedRequestTimeVariants(t *testing.T) {
	// The trailing-field grammar in the wild: no request time at all,
	// '$request_time', '$request_time $upstream_response_time' (first
	// decimal wins), and '-' upstream markers on cache hits.
	cases := []struct {
		tail string
		want float64 // -1 = no latency
	}{
		{``, -1},
		{` 0.042`, 0.042},
		{` 0.250 0.198`, 0.250},
		{` 0.005 -`, 0.005},
	}
	for _, c := range cases {
		ev := mustParseCombined(t,
			`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 5 "-" "ua"`+c.tail)
		if c.want < 0 && ev.HasLatency() {
			t.Errorf("tail %q: latency should be absent, got %v", c.tail, ev.Latency)
		}
		if c.want >= 0 && ev.Latency != c.want {
			t.Errorf("tail %q: latency = %v, want %v", c.tail, ev.Latency, c.want)
		}
	}
}

func TestCombinedCommonFormatWithoutRefererUA(t *testing.T) {
	ev := mustParseCombined(t,
		`10.0.0.9 - - [13/Jul/2026:10:00:00 +0000] "POST /api/orders HTTP/1.1" 201 64`)
	if ev.Method != "POST" || ev.Path != "/api/orders" || ev.Status != 201 {
		t.Fatalf("common format parsed wrong: %+v", ev)
	}
}

func TestCombinedEscapedQuoteInUserAgent(t *testing.T) {
	// nginx escapes embedded quotes; the parser must not end the UA
	// field early and then misread the trailing request time.
	ev := mustParseCombined(t,
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET /x HTTP/1.1" 200 5 "-" "Mozilla \"weird\" agent" 0.033`)
	if ev.Latency != 0.033 {
		t.Fatalf("latency = %v; escaped quote broke field scanning", ev.Latency)
	}
}

func TestCombinedTargetNormalization(t *testing.T) {
	// Query strings are stripped; forward-proxy absolute-form targets
	// are reduced to their path.
	cases := map[string]string{
		"/search?q=cats&page=2":                 "/search",
		"http://api.example.test/v1/things?x=1": "/v1/things",
	}
	for target, want := range cases {
		ev := mustParseCombined(t,
			`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET `+target+` HTTP/1.1" 200 5 "-" "ua"`)
		if ev.Path != want {
			t.Errorf("target %q: path = %q, want %q", target, ev.Path, want)
		}
	}
}

func TestCombinedMalformedRequestLineKept(t *testing.T) {
	// nginx logs 400s for garbage requests with request "-"; those
	// must stay visible in error reports instead of being dropped.
	ev := mustParseCombined(t,
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "-" 400 0 "-" "-"`)
	if ev.Status != 400 || ev.Method != "-" || ev.Path != "(unparsed)" {
		t.Fatalf("malformed request line handled wrong: %+v", ev)
	}
}

func TestCombinedFieldEdgeCases(t *testing.T) {
	// IPv6 remotes parse; "-" bytes means unknown (-1 sentinel);
	// lowercase methods are canonicalized.
	ev := mustParseCombined(t,
		`2001:db8::1 - - [13/Jul/2026:10:00:00 +0000] "get / HTTP/1.1" 304 - "-" "ua"`)
	if ev.Remote != "2001:db8::1" {
		t.Fatalf("remote = %q", ev.Remote)
	}
	if ev.Bytes != -1 {
		t.Fatalf("bytes = %d, want -1 sentinel", ev.Bytes)
	}
	if ev.Method != "GET" {
		t.Fatalf("method = %q, want GET", ev.Method)
	}
}

func TestCombinedTimestampHandling(t *testing.T) {
	// Offsets are honored; a broken clock string degrades to the zero
	// time instead of hiding the request from error-rate analysis.
	ev := mustParseCombined(t,
		`127.0.0.1 - - [13/Jul/2026:19:00:00 +0900] "GET / HTTP/1.1" 200 5`)
	if !ev.Time.Equal(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("time = %v, want 10:00 UTC", ev.Time)
	}
	ev = mustParseCombined(t,
		`127.0.0.1 - - [not-a-time] "GET / HTTP/1.1" 500 5`)
	if !ev.Time.IsZero() || ev.Status != 500 {
		t.Fatalf("lenient timestamp handling broken: %+v", ev)
	}
}

func TestCombinedRejectsGarbage(t *testing.T) {
	for _, line := range []string{
		"",
		"totally not a log line",
		`127.0.0.1 - - "GET / HTTP/1.1" 200 5`, // no [time]
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] 200 5`, // no "request"
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET / HTTP/1.1" whoops 5`,
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET / HTTP/1.1" 999 5`, // out-of-range status
	} {
		if _, err := ParseCombined(line); err == nil {
			t.Errorf("ParseCombined(%q) should fail", line)
		}
	}
}
