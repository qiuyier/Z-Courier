package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestRenderMarkdown(t *testing.T) {
	reports := []reportFile{{
		Path: "reports/loadtest-manual/downlink.json",
		Summary: loadSummary{
			Mode:              "downlink",
			Clients:           10,
			MessagesPerClient: 5,
			Duration:          "1s",
			TargetDuration:    "1s",
			Rate:              50,
			QPS:               49.7,
			ErrorRate:         0.01,
			Passed:            true,
			Counts: summaryCounts{
				DownlinkSuccess:  49,
				DownlinkRejected: 1,
			},
			Latency: map[string]latencySummary{
				"downlink_http": {
					Count: 50,
					P95:   "20ms",
					P99:   "30ms",
				},
			},
			Checks: []thresholdCheck{
				{Name: "min_qps", Passed: true, Actual: 49.7, Expected: 1, Operator: ">="},
				{Name: "max_error_rate", Passed: true, Actual: 0.01, Expected: 0.01, Operator: "<="},
			},
			Failures: map[string]int64{
				"downlink_rejected": 1,
			},
		},
	}}

	markdown := renderMarkdown(reports)
	for _, want := range []string{
		"# Load Test Report",
		"| reports/loadtest-manual/downlink.json | downlink | true | 49.70 | 1.00% | 20ms | 30ms | 1s | 1s | 50 | 10 | 5 |",
		"### reports/loadtest-manual/downlink.json",
		"| min_qps | true | 49.7000 | >= 1.0000 | - |",
		"## Failure Reasons",
		"| reports/loadtest-manual/downlink.json | downlink_rejected | 1 |",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestReadReportsExpandsGlob(t *testing.T) {
	dir := t.TempDir()
	writeSampleReport(t, filepath.Join(dir, "b.json"), "upstream")
	writeSampleReport(t, filepath.Join(dir, "a.json"), "downlink")

	reports, err := readReports([]string{filepath.Join(dir, "*.json")})
	if err != nil {
		t.Fatalf("readReports() error = %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("len(reports) = %d, want 2", len(reports))
	}
	if filepath.Base(reports[0].Path) != "a.json" || reports[0].Summary.Mode != "downlink" {
		t.Fatalf("first report = %+v, want sorted a.json downlink", reports[0])
	}
	if filepath.Base(reports[1].Path) != "b.json" || reports[1].Summary.Mode != "upstream" {
		t.Fatalf("second report = %+v, want sorted b.json upstream", reports[1])
	}
}

func TestReadReportsRejectsMissingGlob(t *testing.T) {
	_, err := readReports([]string{filepath.Join(t.TempDir(), "*.json")})
	if err == nil {
		t.Fatal("readReports() error = nil, want error")
	}
}

func TestWriteOutputCreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "summary.md")
	if err := writeOutput(path, "hello"); err != nil {
		t.Fatalf("writeOutput() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want hello", string(data))
	}
}

func writeSampleReport(t *testing.T, path string, mode string) {
	t.Helper()
	data, err := sonic.Marshal(loadSummary{
		Mode:   mode,
		Passed: true,
		QPS:    10,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
