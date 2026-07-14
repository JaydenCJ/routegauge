// Per-segment heuristics: the fast path of endpoint clustering. Each
// path segment is classified in isolation — numeric IDs, UUIDs, hashes,
// dates, emails and long random tokens become named parameters, so
// /users/123 and /users/456 meet at /users/:id before any cardinality
// analysis runs.
package cluster

import "strings"

// Classify maps one raw path segment to itself or to a parameter
// placeholder. API version segments (v1, v2, …) are deliberately kept
// literal: they are part of the route, not data.
func Classify(seg string) string {
	if seg == "" || isVersion(seg) {
		return seg
	}
	if c, ok := classifyCore(seg); ok {
		return c
	}
	// Extension-aware: /reports/2026-07-06.csv → /reports/:date.csv.
	if stem, ext, ok := splitExt(seg); ok {
		if c, matched := classifyCore(stem); matched {
			return c + "." + ext
		}
	}
	return seg
}

// classifyCore runs the extension-less rules, most specific first.
func classifyCore(seg string) (string, bool) {
	switch {
	case isNumeric(seg):
		return ":id", true
	case isUUID(seg):
		return ":uuid", true
	case isDate(seg):
		return ":date", true
	case isHexHash(seg):
		return ":hash", true
	case strings.Contains(seg, "@"):
		return ":email", true
	case isToken(seg):
		return ":token", true
	}
	return seg, false
}

// isNumeric: every byte a digit — 7, 123, 004212.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isVersion: 'v' followed by 1-3 digits — v1, v2, v10. Kept literal.
func isVersion(s string) bool {
	if len(s) < 2 || len(s) > 4 || (s[0] != 'v' && s[0] != 'V') {
		return false
	}
	return isNumeric(s[1:])
}

// isUUID: canonical 8-4-4-4-12 hyphenated hex, any case.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHexByte(s[i]) {
				return false
			}
		}
	}
	return true
}

// isDate: YYYY-MM-DD with plausible month and day ranges, so an order
// number like 1234-56-78 stays literal.
func isDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	if !isNumeric(s[:4]) || !isNumeric(s[5:7]) || !isNumeric(s[8:]) {
		return false
	}
	month := int(s[5]-'0')*10 + int(s[6]-'0')
	day := int(s[8]-'0')*10 + int(s[9]-'0')
	return month >= 1 && month <= 12 && day >= 1 && day <= 31
}

// isHexHash: ≥12 hex bytes with at least one digit — git SHAs, content
// hashes, trace IDs. The digit requirement keeps hex-alphabet words
// ("deadbeefcafe") literal.
func isHexHash(s string) bool {
	if len(s) < 12 {
		return false
	}
	hasDigit := false
	for i := 0; i < len(s); i++ {
		if !isHexByte(s[i]) {
			return false
		}
		if s[i] >= '0' && s[i] <= '9' {
			hasDigit = true
		}
	}
	return hasDigit
}

// isToken: a long mixed identifier — ≥16 chars of [A-Za-z0-9_-] with at
// least three digits and one letter. Catches session IDs and API keys
// while leaving long English words ("internationalization") literal.
func isToken(s string) bool {
	if len(s) < 16 {
		return false
	}
	digits, letters := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letters++
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return digits >= 3 && letters >= 1
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// splitExt splits "name.ext" when ext is 1-5 alphanumeric bytes and the
// stem is non-empty; used for classified stems like :date.csv.
func splitExt(seg string) (stem, ext string, ok bool) {
	dot := strings.LastIndexByte(seg, '.')
	if dot <= 0 || dot == len(seg)-1 {
		return "", "", false
	}
	ext = seg[dot+1:]
	if len(ext) > 5 {
		return "", "", false
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", "", false
		}
	}
	return seg[:dot], ext, true
}
