package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestRenderMarkdown(t *testing.T) {
	comparison := comparison{
		BasePath:    "reports/baseline/downlink.json",
		CurrentPath: "reports/loadtest-manual/downlink.json",
		Base: loadSummary{
			Mode:              "downlink",
			Clients:           100,
			MessagesPerClient: 10,
			Duration:          "60s",
			TargetDuration:    "60s",
			Rate:              500,
			QPS:               500,
			ErrorRate:         0.02,
			Passed:            true,
			Latency: map[string]latencySummary{
				"downlink_http": {Count: 100, P95MS: 40, P99MS: 80},
			},
		},
		Current: loadSummary{
			Mode:              "downlink",
			Clients:           100,
			MessagesPerClient: 10,
			Duration:          "60s",
			TargetDuration:    "60s",
			Rate:              500,
			QPS:               600,
			ErrorRate:         0.01,
			Passed:            true,
			Latency: map[string]latencySummary{
				"downlink_http": {Count: 100, P95MS: 30, P99MS: 100},
			},
		},
	}

	markdown := renderMarkdown(comparison)
	for _, want := range []string{
		"# Load Test Comparison",
		"| Report | reports/baseline/downlink.json | reports/loadtest-manual/downlink.json |",
		"| QPS | 500.00 | 600.00 | +20.00% | better |",
		"| Error Rate | 2.00% | 1.00% | -50.00% | better |",
		"| p95 | 40.00ms | 30.00ms | -25.00% | better |",
		"| p99 | 80.00ms | 100.00ms | +25.00% | worse |",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestValidateComparisonRejectsModeMismatch(t *testing.T) {
	err := validateComparison(comparison{
		Base:    loadSummary{Mode: "upstream"},
		Current: loadSummary{Mode: "downlink"},
	})
	if err == nil {
		t.Fatal("validateComparison() error = nil, want mode mismatch")
	}
	if !strings.Contains(err.Error(), "mode mismatch") {
		t.Fatalf("error = %q, want mode mismatch", err)
	}
}

func TestReadSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	writeSampleReport(t, path, loadSummary{
		Mode:      "upstream",
		QPS:       42,
		ErrorRate: 0.01,
		Passed:    true,
	})

	summary, err := readSummary(path)
	if err != nil {
		t.Fatalf("readSummary() error = %v", err)
	}
	if summary.Mode != "upstream" || summary.QPS != 42 || summary.ErrorRate != 0.01 || !summary.Passed {
		t.Fatalf("summary = %+v, want upstream qps=42 error_rate=0.01 passed=true", summary)
	}
}

func TestMetricTrend(t *testing.T) {
	tests := []struct {
		name string
		item metricComparison
		want string
	}{
		{
			name: "higher better improved",
			item: metricComparison{Base: 10, Current: 11, BaseValid: true, CurrentValid: true, HigherBetter: true},
			want: "better",
		},
		{
			name: "higher better regressed",
			item: metricComparison{Base: 10, Current: 9, BaseValid: true, CurrentValid: true, HigherBetter: true},
			want: "worse",
		},
		{
			name: "lower better improved",
			item: metricComparison{Base: 10, Current: 9, BaseValid: true, CurrentValid: true, HigherBetter: false},
			want: "better",
		},
		{
			name: "same",
			item: metricComparison{Base: 10, Current: 10, BaseValid: true, CurrentValid: true, HigherBetter: true},
			want: "same",
		},
		{
			name: "missing",
			item: metricComparison{Base: 10, Current: 10, BaseValid: false, CurrentValid: true, HigherBetter: true},
			want: "n/a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := metricTrend(tt.item); got != tt.want {
				t.Fatalf("metricTrend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteOutputCreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "compare.md")
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

func writeSampleReport(t *testing.T, path string, summary loadSummary) {
	t.Helper()
	data, err := sonic.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
