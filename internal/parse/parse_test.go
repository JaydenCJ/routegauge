// Tests for format detection and target normalization shared by both
// parsers.
package parse

import "testing"

func TestDetect(t *testing.T) {
	cases := map[string]Format{
		`{"path":"/x","status":200}`: FormatJSONL,
		`   {"path":"/x"}`:           FormatJSONL, // indentation tolerated
		`127.0.0.1 - - [13/Jul/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 5`: FormatCombined,
		``: FormatCombined, // blank falls back; the line will then be skipped
	}
	for line, want := range cases {
		if got := Detect(line); got != want {
			t.Errorf("Detect(%q) = %s, want %s", line, got, want)
		}
	}
}

func TestValidFormatNames(t *testing.T) {
	for _, ok := range []string{"auto", "combined", "jsonl"} {
		if !ValidFormat(ok) {
			t.Errorf("ValidFormat(%q) should be true", ok)
		}
	}
	for _, bad := range []string{"", "json", "clf", "apache"} {
		if ValidFormat(bad) {
			t.Errorf("ValidFormat(%q) should be false", bad)
		}
	}
}

func TestStripTargetTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/plain", "/plain"},
		{"/q?x=1", "/q"},
		{"/frag#top", "/frag"},
		{"http://example.test/a/b?c=d", "/a/b"},
		{"https://example.test", "/"},
		{"/has://in/path", "/has://in/path"}, // origin-form never carries a scheme
	}
	for _, c := range cases {
		if got := stripTarget(c.in); got != c.want {
			t.Errorf("stripTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEventIsError(t *testing.T) {
	for status, want := range map[int]bool{200: false, 301: false, 400: true, 404: true, 500: true, 503: true} {
		if got := (Event{Status: status}).IsError(); got != want {
			t.Errorf("IsError(%d) = %v, want %v", status, got, want)
		}
	}
}
