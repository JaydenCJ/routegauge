// Combined/common access-log parsing. Hand-rolled (no regexp) so that a
// multi-gigabyte rotation set parses in seconds and quoting edge cases
// (escaped quotes inside the user agent) are handled exactly.
package parse

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// timeLocalLayout is nginx/Apache $time_local: 13/Jul/2026:10:00:00 +0000.
const timeLocalLayout = "02/Jan/2006:15:04:05 -0700"

// ErrNotAccessLog marks a line that does not look like combined/common
// format at all (missing bracketed timestamp or quoted request).
var ErrNotAccessLog = errors.New("line does not match combined/common access-log format")

// ParseCombined parses one combined- or common-format line:
//
//	remote - user [time_local] "METHOD /path HTTP/1.1" status bytes ["referer" "ua" [...]] [request_time ...]
//
// The referer/user-agent pair is optional (common format omits it), and
// any trailing fields are scanned for the first decimal number, which is
// taken as the request time in seconds — matching the widespread nginx
// `combined + $request_time` and `... $upstream_response_time` patterns.
func ParseCombined(line string) (Event, error) {
	s := scanner{line: line}
	ev := Event{Bytes: -1, Latency: -1}

	ev.Remote = s.token()
	if ev.Remote == "" {
		return Event{}, ErrNotAccessLog
	}
	s.token() // identd, conventionally "-"
	s.token() // authenticated user, unused by the report

	ts, ok := s.bracketed()
	if !ok {
		return Event{}, ErrNotAccessLog
	}
	// Lenient on timestamps: a bad clock string should not hide the
	// request from error-rate analysis, so it degrades to "unknown".
	if t, err := time.Parse(timeLocalLayout, ts); err == nil {
		ev.Time = t
	}

	req, ok := s.quoted()
	if !ok {
		return Event{}, ErrNotAccessLog
	}
	ev.Method, ev.Path = splitRequestLine(req)

	statusTok := s.token()
	status, err := strconv.Atoi(statusTok)
	if err != nil || status < 100 || status > 599 {
		return Event{}, errors.New("invalid status field " + strconv.Quote(statusTok))
	}
	ev.Status = status

	if b := s.token(); b != "-" && b != "" {
		if n, err := strconv.ParseInt(b, 10, 64); err == nil {
			ev.Bytes = n
		}
	}

	// Optional quoted referer + user agent (combined), then arbitrary
	// trailing fields. The first decimal-pointed number wins as latency:
	// nginx writes $request_time as seconds with millisecond precision.
	for !s.done() {
		if s.peek() == '"' {
			s.quoted()
			continue
		}
		tok := s.token()
		if ev.Latency < 0 && strings.Contains(tok, ".") {
			if f, err := strconv.ParseFloat(tok, 64); err == nil && f >= 0 {
				ev.Latency = f
			}
		}
	}
	return ev, nil
}

// splitRequestLine splits `METHOD /path HTTP/1.1` and normalizes the
// target. Unusable request lines (nginx logs plain "-" for malformed
// requests) are kept as method "-" path "(unparsed)" so 400s still show
// up in the error report instead of silently vanishing.
func splitRequestLine(req string) (method, path string) {
	fields := strings.Fields(req)
	if len(fields) < 2 {
		return "-", "(unparsed)"
	}
	return strings.ToUpper(fields[0]), stripTarget(fields[1])
}

// scanner walks a log line byte-by-byte.
type scanner struct {
	line string
	pos  int
}

func (s *scanner) done() bool {
	s.skipSpaces()
	return s.pos >= len(s.line)
}

func (s *scanner) peek() byte {
	s.skipSpaces()
	if s.pos < len(s.line) {
		return s.line[s.pos]
	}
	return 0
}

func (s *scanner) skipSpaces() {
	for s.pos < len(s.line) && (s.line[s.pos] == ' ' || s.line[s.pos] == '\t') {
		s.pos++
	}
}

// token reads a run of non-space bytes; empty string means end of line.
func (s *scanner) token() string {
	s.skipSpaces()
	start := s.pos
	for s.pos < len(s.line) && s.line[s.pos] != ' ' && s.line[s.pos] != '\t' {
		s.pos++
	}
	return s.line[start:s.pos]
}

// bracketed reads a `[...]` field, returning its inner text.
func (s *scanner) bracketed() (string, bool) {
	s.skipSpaces()
	if s.pos >= len(s.line) || s.line[s.pos] != '[' {
		return "", false
	}
	s.pos++
	start := s.pos
	for s.pos < len(s.line) && s.line[s.pos] != ']' {
		s.pos++
	}
	if s.pos >= len(s.line) {
		return "", false
	}
	out := s.line[start:s.pos]
	s.pos++ // consume ']'
	return out, true
}

// quoted reads a `"..."` field, honoring backslash escapes (nginx
// escapes embedded quotes as \x22 or \", both survive this loop).
func (s *scanner) quoted() (string, bool) {
	s.skipSpaces()
	if s.pos >= len(s.line) || s.line[s.pos] != '"' {
		return "", false
	}
	s.pos++
	var b strings.Builder
	for s.pos < len(s.line) {
		c := s.line[s.pos]
		if c == '\\' && s.pos+1 < len(s.line) {
			b.WriteByte(s.line[s.pos+1])
			s.pos += 2
			continue
		}
		if c == '"' {
			s.pos++
			return b.String(), true
		}
		b.WriteByte(c)
		s.pos++
	}
	return "", false
}
