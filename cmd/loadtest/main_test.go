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
