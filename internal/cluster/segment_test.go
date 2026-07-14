// Tests for per-segment classification: each rule, and — just as
// important — each near-miss that must stay literal.
package cluster

import "testing"

func TestClassifyParamRules(t *testing.T) {
	cases := map[string]string{
		// numeric IDs
		"7": ":id", "123": ":id", "004212": ":id", "999999999999": ":id",
		// UUIDs, any case
		"9e107d9d-372b-4b6e-8a2f-276173a5f1b3": ":uuid",
		"9E107D9D-372B-4B6E-8A2F-276173A5F1B3": ":uuid",
		// hex hashes: 12-char fingerprints through full git SHAs
		"3f8a92b1c04d": ":hash",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709": ":hash",
		// dates with plausible month/day
		"2026-07-06": ":date",
		// emails
		"alice@example.test": ":email",
		// long mixed tokens: session IDs, API keys (FAKE bodies on purpose)
		"key_FAKE1234EXAMPLE": ":token",
		"sessFAKE000EXAMPLE0": ":token",
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyNearMissesStayLiteral(t *testing.T) {
	// Every rule has a documented near-miss that must NOT match:
	// mis-hyphenated UUIDs, digit-free hex-alphabet words, short hex,
	// impossible dates, long digit-free words, and route vocabulary.
	for _, seg := range []string{
		"deadbeefcafe", "feedface", "abc123", // hex needs len≥12 + a digit
		"1234-56-78", "2026-00-10", "2026-13-01", "2026-07-32", // impossible dates
		"internationalization", "recommendations", "user-preferences", // no digits
		"users", "orders", "api", "health", "static", "index.html",
	} {
		if got := Classify(seg); got != seg {
			t.Errorf("Classify(%q) = %q, want literal", seg, got)
		}
	}
	// Right length, wrong hyphen placement: anything but :uuid.
	if got := Classify("9e107d9d372b-4b6e-8a2f-276173a5f1b30"); got == ":uuid" {
		t.Error("mis-hyphenated UUID must not match :uuid")
	}
}

func TestClassifyVersionSegmentsStayLiteral(t *testing.T) {
	// v1/v2 are part of the route contract, not data — even though
	// their tail is numeric.
	for _, seg := range []string{"v1", "v2", "V3", "v10", "v999"} {
		if got := Classify(seg); got != seg {
			t.Errorf("Classify(%q) = %q, want literal", seg, got)
		}
	}
}

func TestClassifyExtensionAware(t *testing.T) {
	cases := map[string]string{
		"2026-07-06.csv": ":date.csv",
		"1042.json":      ":id.json",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709.tar": ":hash.tar",
		"report.pdf": "report.pdf", // literal stem keeps its name
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
}
