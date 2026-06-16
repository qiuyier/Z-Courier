package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

func TestLatencyRecorderSummary(t *testing.T) {
	var recorder latencyRecorder
	for i := 1; i <= 100; i++ {
		recorder.Record(time.Duration(i) * time.Millisecond)
	}

	summary := recorder.Summary()
	if summary.Count != 100 {
		t.Fatalf("Count = %d, want 100", summary.Count)
	}
	if summary.Min != "1ms" || summary.Max != "100ms" {
		t.Fatalf("min/max = %s/%s, want 1ms/100ms", summary.Min, summary.Max)
	}
	if summary.P50MS != 50 || summary.P95MS != 95 || summary.P99MS != 99 {
		t.Fatalf("percentiles = p50:%s p95:%s p99:%s, want 50ms/95ms/99ms", summary.P50, summary.P95, summary.P99)
	}
}

func TestConfigOverallTimeout(t *testing.T) {
	cfg := config{Timeout: 30 * time.Second}
	if cfg.DurationMode() {
		t.Fatal("DurationMode() = true, want false")
	}
	if got := cfg.OverallTimeout(); got != 30*time.Second {
		t.Fatalf("OverallTimeout() = %s, want 30s", got)
	}

	cfg.RunDuration = time.Minute
	if !cfg.DurationMode() {
		t.Fatal("DurationMode() = false, want true")
	}
	if got := cfg.OverallTimeout(); got != 90*time.Second {
		t.Fatalf("OverallTimeout() = %s, want 90s", got)
	}
}

func TestRateInterval(t *testing.T) {
	if got := rateInterval(1000); got != time.Millisecond {
		t.Fatalf("rateInterval(1000) = %s, want 1ms", got)
	}
	if got := rateInterval(0); got != time.Second {
		t.Fatalf("rateInterval(0) = %s, want 1s", got)
	}
}

func TestBuildSummary(t *testing.T) {
	cfg := config{
		Mode:              "downlink",
		Clients:           10,
		MessagesPerClient: 20,
		HTTPConcurrency:   5,
		RunDuration:       time.Minute,
		Rate:              100,
	}
	counts := &counters{}
	counts.downlinkSuccess.Store(123)
	counts.downlinkRejected.Store(4)
	counts.sendErrors.Store(2)
	counts.downlinkLatency.Record(5 * time.Millisecond)
	counts.failures.Add("overloaded")
	counts.failures.Add("overloaded")

	summary := buildSummary(cfg, counts, 2*time.Second, nil)
	if summary.Mode != "downlink" || summary.TargetDuration != "1m0s" || summary.Rate != 100 {
		t.Fatalf("summary mode/duration/rate = %+v", summary)
	}
	if summary.QPS != 63.5 {
		t.Fatalf("QPS = %v, want 63.5", summary.QPS)
	}
	if !summary.Passed {
		t.Fatalf("Passed = false, want true")
	}
	if summary.Counts.DownlinkSuccess != 123 || summary.Counts.DownlinkRejected != 4 || summary.Counts.SendErrors != 2 {
		t.Fatalf("counts = %+v", summary.Counts)
	}
	if summary.Latency["downlink_http"].P50MS != 5 {
		t.Fatalf("downlink p50 = %+v, want 5ms", summary.Latency["downlink_http"])
	}
	if summary.Failures["overloaded"] != 2 {
		t.Fatalf("failures = %+v, want overloaded=2", summary.Failures)
	}
}

func TestBuildSummaryThresholdChecksPass(t *testing.T) {
	cfg := config{
		Mode:            "downlink",
		Clients:         10,
		HTTPConcurrency: 5,
		MinQPS:          90,
		MaxP95MS:        10,
		MaxP99MS:        10,
		MaxErrorRate:    0.02,
		MaxErrorRateSet: true,
	}
	counts := &counters{}
	counts.downlinkSuccess.Store(99)
	counts.downlinkRejected.Store(1)
	counts.downlinkLatency.Record(8 * time.Millisecond)

	summary := buildSummary(cfg, counts, time.Second, nil)
	if !summary.Passed {
		t.Fatalf("Passed = false, want true; checks=%+v", summary.Checks)
	}
	if summary.ErrorRate != 0.01 {
		t.Fatalf("ErrorRate = %v, want 0.01", summary.ErrorRate)
	}
	if len(summary.Checks) != 4 {
		t.Fatalf("len(checks) = %d, want 4", len(summary.Checks))
	}
	for _, check := range summary.Checks {
		if !check.Passed {
			t.Fatalf("check %+v failed, want all pass", check)
		}
	}
}

func TestBuildSummaryThresholdChecksFail(t *testing.T) {
	cfg := config{
		Mode:            "downlink",
		MinQPS:          200,
		MaxP95MS:        1,
		MaxErrorRate:    0,
		MaxErrorRateSet: true,
	}
	counts := &counters{}
	counts.downlinkSuccess.Store(99)
	counts.downlinkRejected.Store(1)
	counts.downlinkLatency.Record(8 * time.Millisecond)

	summary := buildSummary(cfg, counts, time.Second, nil)
	if summary.Passed {
		t.Fatalf("Passed = true, want false; checks=%+v", summary.Checks)
	}
	if !summary.hasFailedChecks() {
		t.Fatalf("hasFailedChecks() = false, want true")
	}
	failed := map[string]bool{}
	for _, check := range summary.Checks {
		if !check.Passed {
			failed[check.Name] = true
		}
	}
	if !failed["min_qps"] || !failed["max_p95_ms"] || !failed["max_error_rate"] {
		t.Fatalf("failed checks = %+v, want min_qps/max_p95_ms/max_error_rate", failed)
	}
}

func TestBuildSummaryMissingLatencyThresholdFails(t *testing.T) {
	cfg := config{
		Mode:     "upstream",
		MaxP95MS: 10,
	}
	counts := &counters{}
	counts.upstreamAccepted.Store(1)

	summary := buildSummary(cfg, counts, time.Second, nil)
	if summary.Passed {
		t.Fatalf("Passed = true, want false")
	}
	if len(summary.Checks) != 1 {
		t.Fatalf("len(checks) = %d, want 1", len(summary.Checks))
	}
	if summary.Checks[0].Passed || summary.Checks[0].Reason == "" {
		t.Fatalf("check = %+v, want failed check with reason", summary.Checks[0])
	}
}

func TestWriteReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "loadtest.json")
	want := summary{
		Mode:           "upstream",
		Clients:        2,
		Duration:       "1s",
		DurationMillis: 1000,
		QPS:            42.5,
		Counts: summaryCounts{
			UpstreamAccepted: 85,
		},
	}

	if err := writeReport(path, want); err != nil {
		t.Fatalf("writeReport() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got summary
	if err := sonic.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Mode != want.Mode || got.QPS != want.QPS || got.Counts.UpstreamAccepted != want.Counts.UpstreamAccepted {
		t.Fatalf("report = %+v, want %+v", got, want)
	}
}

func TestReasonCountsSnapshotSortsByCount(t *testing.T) {
	var counts reasonCounts
	counts.Add("timeout")
	counts.Add("overloaded")
	counts.Add("timeout")

	items := counts.Snapshot()
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].Reason != "timeout" || items[0].Count != 2 {
		t.Fatalf("first item = %+v, want timeout=2", items[0])
	}
	if items[1].Reason != "overloaded" || items[1].Count != 1 {
		t.Fatalf("second item = %+v, want overloaded=1", items[1])
	}
}

func TestCodeFromHTTPResponse(t *testing.T) {
	if got := codeFromHTTPResponse(429, []byte(`{"code":"overloaded"}`)); got != "overloaded" {
		t.Fatalf("codeFromHTTPResponse() = %q, want overloaded", got)
	}
	if got := codeFromHTTPResponse(502, []byte(`not-json`)); got != "status_502" {
		t.Fatalf("codeFromHTTPResponse() = %q, want status_502", got)
	}
}

func TestReasonFromError(t *testing.T) {
	if got := reasonFromError(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("reasonFromError(deadline) = %q, want timeout", got)
	}
	if got := reasonFromError(context.Canceled); got != "context_canceled" {
		t.Fatalf("reasonFromError(canceled) = %q, want context_canceled", got)
	}
}
