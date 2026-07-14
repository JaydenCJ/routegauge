// In-process CLI integration tests: Run() is exercised end-to-end
// against fixture logs written into temp dirs, asserting on output and
// exit codes without building a binary.
package cli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/routegauge/internal/version"
)

// combinedFixture is a deterministic mixed-traffic log in combined
// format with trailing $request_time.
func combinedFixture(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	line := func(sec int, method, path string, status int, latency string) {
		fmt.Fprintf(&b,
			"127.0.0.1 - - [06/Jul/2026:09:00:%02d +0000] \"%s %s HTTP/1.1\" %d 512 \"-\" \"smoke-agent/1.0\" %s\n",
			sec, method, path, status, latency)
	}
	for i := 0; i < 8; i++ {
		line(i, "GET", fmt.Sprintf("/api/users/%d", 100+i), 200, "0.020")
	}
	line(8, "GET", "/api/users/999", 500, "0.900")
	line(9, "POST", "/api/orders", 201, "0.150")
	line(10, "GET", "/api/orders/9e107d9d-372b-4b6e-8a2f-276173a5f1b3", 404, "0.010")
	line(11, "GET", "/health", 200, "0.001")
	b.WriteString("not a log line at all\n")
	p := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// run invokes the CLI in-process and returns exit code + streams.
func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersionOutputs(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, out, _ := run(args...)
		if code != ExitOK || out != "routegauge "+version.Version+"\n" {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func TestHelpAndTopLevelUsage(t *testing.T) {
	code, out, _ := run("help")
	if code != ExitOK || !strings.Contains(out, "usage:") {
		t.Fatalf("help: code=%d", code)
	}
	code, _, errOut := run()
	if code != ExitUsage || !strings.Contains(errOut, "usage:") {
		t.Fatalf("no args: code=%d", code)
	}
	if code, _, _ := run("--wat"); code != ExitUsage {
		t.Fatalf("unknown flag: code=%d", code)
	}
}

func TestReportClustersAndCounts(t *testing.T) {
	code, out, errOut := run("report", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("report failed (%d): %s", code, errOut)
	}
	for _, want := range []string{
		"12 requests",
		"skipped: 1 unparseable line",
		"/api/users/:id",
		"/api/orders/:uuid",
		"GET", "POST",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/api/users/100") {
		t.Fatalf("raw path leaked into clustered report:\n%s", out)
	}
	// A bare file argument defaults to the report subcommand.
	code, out, _ = run(combinedFixture(t))
	if code != ExitOK || !strings.Contains(out, "routegauge report") {
		t.Fatalf("bare file arg: code=%d out=%q", code, out)
	}
}

func TestReportJSONIsDecodableAndAccurate(t *testing.T) {
	code, out, _ := run("report", "--format", "json", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("json report failed: %d", code)
	}
	var doc struct {
		Requests     int `json:"requests"`
		SkippedLines int `json:"skipped_lines"`
		Routes       []struct {
			Method  string `json:"method"`
			Route   string `json:"route"`
			Latency *struct {
				P95Ms float64 `json:"p95_ms"`
			} `json:"latency"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Requests != 12 || doc.SkippedLines != 1 {
		t.Fatalf("totals wrong: %+v", doc)
	}
	if doc.Routes[0].Route != "/api/users/:id" || doc.Routes[0].Latency == nil {
		t.Fatalf("first route wrong: %+v", doc.Routes[0])
	}
	// 9 samples of the users route: eight 0.020 and one 0.900 → p95
	// nearest-rank picks rank ceil(8.55)=9, the 900 ms outlier.
	if doc.Routes[0].Latency.P95Ms != 900 {
		t.Fatalf("p95_ms = %v, want 900", doc.Routes[0].Latency.P95Ms)
	}
}

func TestReportMarkdown(t *testing.T) {
	code, out, _ := run("report", "--format", "markdown", combinedFixture(t))
	if code != ExitOK || !strings.Contains(out, "| Method | Endpoint |") {
		t.Fatalf("markdown report: code=%d\n%s", code, out)
	}
}

func TestEndpointsShowsSamplePaths(t *testing.T) {
	code, out, _ := run("endpoints", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("endpoints failed: %d", code)
	}
	if !strings.Contains(out, "e.g. /api/users/100") {
		t.Fatalf("sample raw paths missing:\n%s", out)
	}
}

func TestErrorsFocusesOnFailures(t *testing.T) {
	code, out, _ := run("errors", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("errors failed: %d", code)
	}
	for _, want := range []string{"2 error responses", "500", "404"} {
		if !strings.Contains(out, want) {
			t.Errorf("errors view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/health") {
		t.Fatalf("clean route leaked into errors view:\n%s", out)
	}
}

func TestCheckErrorRateGate(t *testing.T) {
	// 2 errors of 12 = 16.7%: passes at 50%, breaches at 10%.
	fixture := combinedFixture(t)
	code, out, _ := run("check", "--max-error-rate", "50", fixture)
	if code != ExitOK || !strings.Contains(out, "check: OK") {
		t.Fatalf("check should pass: code=%d\n%s", code, out)
	}
	code, out, _ = run("check", "--max-error-rate", "10", fixture)
	if code != ExitBreach || !strings.Contains(out, "check: FAIL") {
		t.Fatalf("check should breach: code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "BREACH") {
		t.Fatalf("verdict line missing:\n%s", out)
	}
}

func TestCheckLatencyGate(t *testing.T) {
	// Overall p95 is 900ms (rank 12 of 12 sorted ≈ the outlier).
	code, out, _ := run("check", "--max-p95", "100ms", combinedFixture(t))
	if code != ExitBreach {
		t.Fatalf("p95 gate should breach: code=%d\n%s", code, out)
	}
	code, _, _ = run("check", "--max-p95", "2s", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("p95 gate should pass at 2s: code=%d", code)
	}
}

func TestCheckPerRoute(t *testing.T) {
	// Overall error rate is 16.7%, but the users route alone is 1/9 =
	// 11.1%; per-route mode with min-requests 5 must flag it.
	code, out, _ := run("check", "--per-route", "--min-requests", "5",
		"--max-error-rate", "5", combinedFixture(t))
	if code != ExitBreach || !strings.Contains(out, "route GET /api/users/:id") {
		t.Fatalf("per-route breach missing: code=%d\n%s", code, out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		hint string // required stderr substring, "" = any
	}{
		{"check without limits", []string{"check", "x.log"}, "no limits"},
		{"bad duration", []string{"check", "--max-p95", "fast", "x.log"}, ""},
		{"bad --format", []string{"report", "--format", "yaml", "x.log"}, ""},
		{"markdown outside report", []string{"endpoints", "--format", "markdown", "x.log"}, "only available for report"},
		{"bad --sort", []string{"report", "--sort", "vibes", "x.log"}, ""},
		{"bad --since", []string{"report", "--since", "yesterday", "x.log"}, ""},
		{"no files", []string{"report"}, "no input files"},
		{"bad --log-format", []string{"report", "--log-format", "clf", "x.log"}, "invalid --log-format"},
	}
	for _, c := range cases {
		code, _, errOut := run(c.args...)
		if code != ExitUsage {
			t.Errorf("%s: code=%d, want %d", c.name, code, ExitUsage)
		}
		if c.hint != "" && !strings.Contains(errOut, c.hint) {
			t.Errorf("%s: stderr %q missing %q", c.name, errOut, c.hint)
		}
	}
}

func TestRuntimeErrorsExitThree(t *testing.T) {
	code, _, _ := run("report", filepath.Join(t.TempDir(), "ghost.log"))
	if code != ExitRuntime {
		t.Fatalf("missing file: code=%d, want %d", code, ExitRuntime)
	}
	p := filepath.Join(t.TempDir(), "garbage.log")
	if err := os.WriteFile(p, []byte("junk\nmore junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := run("report", p)
	if code != ExitRuntime || !strings.Contains(errOut, "check --log-format") {
		t.Fatalf("all-unparseable input: code=%d %q", code, errOut)
	}
}

func TestStdinInput(t *testing.T) {
	old := Stdin
	defer func() { Stdin = old }()
	Stdin = strings.NewReader(
		`127.0.0.1 - - [06/Jul/2026:09:00:00 +0000] "GET /ping HTTP/1.1" 200 2 "-" "ua" 0.001` + "\n")
	code, out, _ := run("report", "-")
	if code != ExitOK || !strings.Contains(out, "/ping") {
		t.Fatalf("stdin report: code=%d\n%s", code, out)
	}
}

func TestGzipInput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log.1.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	fmt.Fprintln(gz, `127.0.0.1 - - [06/Jul/2026:09:00:00 +0000] "GET /zipped HTTP/1.1" 200 2 "-" "ua" 0.001`)
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("report", p)
	if code != ExitOK || !strings.Contains(out, "/zipped") {
		t.Fatalf("gzip report: code=%d\n%s", code, out)
	}
}

func TestMultipleFilesMerged(t *testing.T) {
	f1 := combinedFixture(t)
	f2 := combinedFixture(t)
	code, out, _ := run("report", f1, f2)
	if code != ExitOK || !strings.Contains(out, "24 requests") {
		t.Fatalf("two files: code=%d\n%s", code, out)
	}
}

func TestJSONLInputAutoDetected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.jsonl")
	lines := `{"time":"2026-07-06T09:00:00Z","method":"GET","path":"/items/42","status":200,"duration_ms":15}
{"time":"2026-07-06T09:00:01Z","method":"GET","path":"/items/43","status":500,"duration_ms":220}
`
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("report", p)
	if code != ExitOK || !strings.Contains(out, "/items/:id") {
		t.Fatalf("jsonl auto-detect: code=%d\n%s", code, out)
	}
}

func TestWrongForcedFormatFailsLoudly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.jsonl")
	if err := os.WriteFile(p, []byte(`{"path":"/x","status":200}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ := run("report", "--log-format", "combined", p)
	if code != ExitRuntime {
		t.Fatalf("forced wrong format: code=%d, want runtime error", code)
	}
}

func TestMethodFilter(t *testing.T) {
	code, out, _ := run("report", "--method", "post", combinedFixture(t))
	if code != ExitOK || !strings.Contains(out, "1 request, 1 route") {
		t.Fatalf("method filter: code=%d\n%s", code, out)
	}
}

func TestPathPrefixFilterMatchesWholeSegments(t *testing.T) {
	code, out, _ := run("report", "--path-prefix", "/api", combinedFixture(t))
	if code != ExitOK {
		t.Fatalf("prefix filter failed: %d", code)
	}
	if strings.Contains(out, "/health") {
		t.Fatalf("/health should be filtered out:\n%s", out)
	}
	if !strings.Contains(out, "11 requests") {
		t.Fatalf("prefix kept wrong count:\n%s", out)
	}
}

func TestSinceUntilWindow(t *testing.T) {
	// Fixture seconds run 09:00:00–09:00:11; keep [09:00:08, 09:00:10).
	code, out, _ := run("report",
		"--since", "2026-07-06T09:00:08Z",
		"--until", "2026-07-06T09:00:10Z",
		combinedFixture(t))
	if code != ExitOK || !strings.Contains(out, "2 requests") {
		t.Fatalf("time window: code=%d\n%s", code, out)
	}
}

func TestNoClusterKeepsRawPaths(t *testing.T) {
	code, out, _ := run("report", "--no-cluster", "--top", "0", combinedFixture(t))
	if code != ExitOK || !strings.Contains(out, "/api/users/100") {
		t.Fatalf("--no-cluster: code=%d\n%s", code, out)
	}
	if strings.Contains(out, ":id") {
		t.Fatalf("params present despite --no-cluster:\n%s", out)
	}
}

func TestClusterThresholdFlag(t *testing.T) {
	// Six distinct slugs: default threshold keeps them, threshold 3
	// collapses them into :param.
	var b strings.Builder
	for _, slug := range []string{"alpha", "bravo", "carrot", "delta", "echo", "fox"} {
		fmt.Fprintf(&b,
			"127.0.0.1 - - [06/Jul/2026:09:00:00 +0000] \"GET /kb/%s HTTP/1.1\" 200 10 \"-\" \"ua\" 0.005\n", slug)
	}
	p := filepath.Join(t.TempDir(), "kb.log")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := run("report", p)
	if !strings.Contains(out, "/kb/alpha") {
		t.Fatalf("default threshold should keep slugs:\n%s", out)
	}
	_, out, _ = run("report", "--cluster-threshold", "3", p)
	if !strings.Contains(out, "/kb/:param") {
		t.Fatalf("threshold 3 should collapse slugs:\n%s", out)
	}
}

func TestSortP95PutsSlowRouteFirst(t *testing.T) {
	_, out, _ := run("report", "--sort", "p95", combinedFixture(t))
	users := strings.Index(out, "/api/users/:id")
	health := strings.Index(out, "/health")
	if users < 0 || health < 0 || users > health {
		t.Fatalf("p95 sort order wrong:\n%s", out)
	}
}
