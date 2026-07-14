// JSON-lines access-log parsing. Structured loggers disagree on field
// names and latency units, so extraction is driven by documented alias
// tables (README "JSON field aliases") instead of a fixed schema.
package parse

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Field aliases, checked in order; the first present key wins.
var (
	timeKeys   = []string{"time", "timestamp", "@timestamp", "ts", "datetime", "date"}
	methodKeys = []string{"method", "request_method", "verb", "http_method"}
	pathKeys   = []string{"path", "uri", "request_uri", "url", "request_path"}
	statusKeys = []string{"status", "status_code", "code", "response_status", "response_code"}
	bytesKeys  = []string{"bytes", "body_bytes_sent", "bytes_sent", "response_size", "size"}
	remoteKeys = []string{"remote_addr", "client_ip", "remote_ip", "remote", "ip"}
)

// latencyKeys maps field names to the multiplier that converts their
// numeric value to seconds. Names carry the unit; bare names mean
// seconds (nginx $request_time convention).
var latencyKeys = []struct {
	key   string
	scale float64
}{
	{"request_time", 1}, {"duration", 1}, {"latency", 1},
	{"response_time", 1}, {"elapsed", 1}, {"duration_s", 1},
	{"duration_ms", 1e-3}, {"latency_ms", 1e-3}, {"response_time_ms", 1e-3},
	{"request_time_ms", 1e-3}, {"elapsed_ms", 1e-3}, {"time_taken_ms", 1e-3},
	{"duration_us", 1e-6}, {"latency_us", 1e-6},
	{"duration_ns", 1e-9}, {"latency_ns", 1e-9},
}

// ParseJSONL parses one JSON object into an Event. A line without a
// usable path (or request line) or status is rejected, because neither
// clustering nor error rates can be computed for it.
func ParseJSONL(line string) (Event, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return Event{}, err
	}
	ev := Event{Bytes: -1, Latency: -1}

	if s, ok := firstString(m, methodKeys); ok {
		ev.Method = strings.ToUpper(s)
	}
	if s, ok := firstString(m, pathKeys); ok {
		ev.Path = stripTarget(s)
	}
	// Fall back to a combined-style request line: "GET /x HTTP/1.1".
	if req, ok := m["request"].(string); ok && (ev.Path == "" || ev.Method == "") {
		method, path := splitRequestLine(req)
		if ev.Method == "" {
			ev.Method = method
		}
		if ev.Path == "" {
			ev.Path = path
		}
	}
	if ev.Path == "" {
		return Event{}, errors.New("json record has no path field")
	}
	if ev.Method == "" {
		ev.Method = "-"
	}

	status, ok := firstNumber(m, statusKeys)
	if !ok || status < 100 || status > 599 {
		return Event{}, errors.New("json record has no valid status field")
	}
	ev.Status = int(status)

	if b, ok := firstNumber(m, bytesKeys); ok && b >= 0 {
		ev.Bytes = int64(b)
	}
	if r, ok := firstString(m, remoteKeys); ok {
		ev.Remote = r
	}
	ev.Time = extractTime(m)
	ev.Latency = extractLatency(m)
	return ev, nil
}

// extractLatency resolves the first known latency field to seconds.
// String values that parse as Go durations ("12.3ms") are honored
// regardless of the field's implied unit.
func extractLatency(m map[string]any) float64 {
	for _, lk := range latencyKeys {
		v, present := m[lk.key]
		if !present {
			continue
		}
		if s, ok := v.(string); ok {
			if d, err := time.ParseDuration(s); err == nil && d >= 0 {
				return d.Seconds()
			}
		}
		if f, ok := toNumber(v); ok && f >= 0 {
			return f * lk.scale
		}
	}
	return -1
}

// extractTime resolves the first known timestamp field. Numeric epochs
// are scaled by magnitude: seconds, milliseconds, microseconds, or
// nanoseconds. Unknown formats degrade to the zero time.
func extractTime(m map[string]any) time.Time {
	for _, k := range timeKeys {
		v, present := m[k]
		if !present {
			continue
		}
		switch t := v.(type) {
		case string:
			if parsed, ok := parseTimeString(t); ok {
				return parsed
			}
		default:
			if f, ok := toNumber(v); ok && f > 0 {
				return epochToTime(f)
			}
		}
	}
	return time.Time{}
}

var stringTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999", // RFC3339 without zone → UTC
	"2006-01-02 15:04:05",
	timeLocalLayout,
}

func parseTimeString(s string) (time.Time, bool) {
	for _, layout := range stringTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	// Epoch written as a string ("1783075200" or "1783075200.25").
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return epochToTime(f), true
	}
	return time.Time{}, false
}

// epochToTime converts a numeric epoch to a time, inferring the unit
// from magnitude: <1e11 seconds, <1e14 ms, <1e17 µs, otherwise ns.
func epochToTime(f float64) time.Time {
	switch {
	case f < 1e11: // seconds (covers dates through year 5138)
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	case f < 1e14: // milliseconds
		return time.UnixMilli(int64(f)).UTC()
	case f < 1e17: // microseconds
		return time.UnixMicro(int64(f)).UTC()
	default: // nanoseconds
		return time.Unix(0, int64(f)).UTC()
	}
}

func firstString(m map[string]any, keys []string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

func firstNumber(m map[string]any, keys []string) (float64, bool) {
	for _, k := range keys {
		if v, present := m[k]; present {
			if f, ok := toNumber(v); ok {
				return f, true
			}
		}
	}
	return 0, false
}

// toNumber accepts JSON numbers and numeric strings ("200" statuses are
// common in logstash pipelines).
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
