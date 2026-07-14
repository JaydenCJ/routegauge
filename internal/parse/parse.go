// Package parse turns raw access-log lines into structured Events. It
// supports the nginx/Apache "combined" and "common" formats (with the
// widespread request-time extension) and JSON-lines logs, and can
// auto-detect which of the two families a stream uses.
package parse

import "time"

// Event is one parsed access-log record. Fields that a given log format
// does not carry are left at their documented zero/sentinel values so the
// aggregation layer can distinguish "absent" from "zero".
type Event struct {
	// Time is the request timestamp; the zero value means unknown.
	Time time.Time
	// Method is the upper-case HTTP method, or "-" when the logged
	// request line was unparseable (nginx logs 400s with request "-").
	Method string
	// Path is the URL path with query string and fragment stripped.
	// "(unparsed)" marks records whose request line was unusable.
	Path string
	// Status is the numeric HTTP status code.
	Status int
	// Bytes is the response body size; -1 means unknown.
	Bytes int64
	// Latency is the request duration in seconds; a negative value
	// means the log line carried no latency field.
	Latency float64
	// Remote is the client address as logged, possibly empty.
	Remote string
}

// HasLatency reports whether the event carries a usable latency sample.
func (e Event) HasLatency() bool { return e.Latency >= 0 }

// IsError reports whether the response status is a 4xx or 5xx.
func (e Event) IsError() bool { return e.Status >= 400 && e.Status <= 599 }

// Format identifies a log line dialect.
type Format string

const (
	// FormatAuto sniffs the first non-blank line: '{' selects JSON
	// lines, anything else the combined/common family.
	FormatAuto Format = "auto"
	// FormatCombined covers both nginx/Apache "combined" and "common"
	// access logs, with optional trailing request-time fields.
	FormatCombined Format = "combined"
	// FormatJSONL covers structured logs with one JSON object per line.
	FormatJSONL Format = "jsonl"
)

// ValidFormat reports whether s names a supported log format.
func ValidFormat(s string) bool {
	switch Format(s) {
	case FormatAuto, FormatCombined, FormatJSONL:
		return true
	}
	return false
}

// Detect sniffs the format of a single log line. It never returns
// FormatAuto.
func Detect(line string) Format {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ', '\t':
			continue
		case '{':
			return FormatJSONL
		default:
			return FormatCombined
		}
	}
	return FormatCombined
}

// Line parses one log line in the given concrete format. Callers using
// FormatAuto must resolve it with Detect first.
func Line(line string, f Format) (Event, error) {
	if f == FormatJSONL {
		return ParseJSONL(line)
	}
	return ParseCombined(line)
}

// stripTarget reduces a request target to its bare path: absolute-form
// URLs (proxy logs) lose scheme and host, and query/fragment are cut.
func stripTarget(target string) string {
	// Absolute form: "http://host/path" or "https://host/path". A
	// target already starting with "/" is origin-form and cannot carry
	// a scheme, even if "://" appears inside the path.
	if i := indexSchemeSep(target); i >= 0 && !hasSlashPrefix(target) {
		rest := target[i+3:]
		if j := indexByte(rest, '/'); j >= 0 {
			target = rest[j:]
		} else {
			target = "/"
		}
	}
	for i := 0; i < len(target); i++ {
		if target[i] == '?' || target[i] == '#' {
			return target[:i]
		}
	}
	return target
}

// indexSchemeSep finds "://" when it appears within the first few bytes,
// i.e. an actual URL scheme rather than a path that embeds the sequence.
func indexSchemeSep(s string) int {
	limit := len(s)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i+2 < len(s) && i < limit; i++ {
		if s[i] == ':' && s[i+1] == '/' && s[i+2] == '/' {
			return i
		}
	}
	return -1
}

func hasSlashPrefix(s string) bool { return len(s) > 0 && s[0] == '/' }

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
