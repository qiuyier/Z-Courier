package main

import (
	"context"
	"testing"
	"time"
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
	if summary.Min != time.Millisecond || summary.Max != 100*time.Millisecond {
		t.Fatalf("min/max = %s/%s, want 1ms/100ms", summary.Min, summary.Max)
	}
	if summary.P50 != 50*time.Millisecond || summary.P95 != 95*time.Millisecond || summary.P99 != 99*time.Millisecond {
		t.Fatalf("percentiles = p50:%s p95:%s p99:%s, want 50ms/95ms/99ms", summary.P50, summary.P95, summary.P99)
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
