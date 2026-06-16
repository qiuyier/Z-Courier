package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/bytedance/sonic"
)

type config struct {
	BasePath    string
	CurrentPath string
	OutputPath  string
}

type loadSummary struct {
	Mode              string                    `json:"mode"`
	Clients           int                       `json:"clients"`
	MessagesPerClient int                       `json:"messages_per_client"`
	Duration          string                    `json:"duration"`
	TargetDuration    string                    `json:"target_duration,omitempty"`
	Rate              int                       `json:"rate,omitempty"`
	QPS               float64                   `json:"qps"`
	ErrorRate         float64                   `json:"error_rate"`
	Latency           map[string]latencySummary `json:"latency,omitempty"`
	Passed            bool                      `json:"passed"`
}

type latencySummary struct {
	Count int     `json:"count"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

type comparison struct {
	BasePath    string
	CurrentPath string
	Base        loadSummary
	Current     loadSummary
}

type metricComparison struct {
	Name         string
	Base         float64
	Current      float64
	BaseValid    bool
	CurrentValid bool
	Kind         metricKind
	HigherBetter bool
}

type metricKind string

const (
	metricNumber  metricKind = "number"
	metricPercent metricKind = "percent"
	metricMillis  metricKind = "millis"
)

func main() {
	cfg := parseFlags()
	base, err := readSummary(cfg.BasePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadcompare failed: %v\n", err)
		os.Exit(1)
	}
	current, err := readSummary(cfg.CurrentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadcompare failed: %v\n", err)
		os.Exit(1)
	}

	comparison := comparison{
		BasePath:    cfg.BasePath,
		CurrentPath: cfg.CurrentPath,
		Base:        base,
		Current:     current,
	}
	if err := validateComparison(comparison); err != nil {
		fmt.Fprintf(os.Stderr, "loadcompare failed: %v\n", err)
		os.Exit(1)
	}

	markdown := renderMarkdown(comparison)
	if cfg.OutputPath == "" {
		fmt.Print(markdown)
		return
	}
	if err := writeOutput(cfg.OutputPath, markdown); err != nil {
		fmt.Fprintf(os.Stderr, "write comparison failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	fs := flag.NewFlagSet("loadcompare", flag.ExitOnError)
	fs.StringVar(&cfg.BasePath, "base", "", "baseline loadtest JSON report")
	fs.StringVar(&cfg.CurrentPath, "current", "", "current loadtest JSON report")
	fs.StringVar(&cfg.OutputPath, "output", "", "write Markdown comparison to this file; stdout when empty")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: loadcompare -base baseline.json -current current.json [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if cfg.BasePath == "" || cfg.CurrentPath == "" {
		fs.Usage()
		os.Exit(2)
	}

	return cfg
}

func readSummary(path string) (loadSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return loadSummary{}, fmt.Errorf("read %s: %w", path, err)
	}

	var summary loadSummary
	if err := sonic.Unmarshal(data, &summary); err != nil {
		return loadSummary{}, fmt.Errorf("decode %s: %w", path, err)
	}

	return summary, nil
}

func validateComparison(comparison comparison) error {
	if comparison.Base.Mode == "" {
		return fmt.Errorf("baseline report %s has empty mode", comparison.BasePath)
	}
	if comparison.Current.Mode == "" {
		return fmt.Errorf("current report %s has empty mode", comparison.CurrentPath)
	}
	if comparison.Base.Mode != comparison.Current.Mode {
		return fmt.Errorf("mode mismatch: baseline=%s current=%s", comparison.Base.Mode, comparison.Current.Mode)
	}

	return nil
}

func renderMarkdown(comparison comparison) string {
	var b strings.Builder
	b.WriteString("# Load Test Comparison\n\n")
	writeOverview(&b, comparison)
	writeMetrics(&b, comparisonMetrics(comparison))
	return b.String()
}

func writeOverview(b *strings.Builder, comparison comparison) {
	b.WriteString("| Field | Baseline | Current |\n")
	b.WriteString("|---|---:|---:|\n")
	b.WriteString(fmt.Sprintf("| Report | %s | %s |\n", escapeCell(comparison.BasePath), escapeCell(comparison.CurrentPath)))
	b.WriteString(fmt.Sprintf("| Mode | %s | %s |\n", escapeCell(comparison.Base.Mode), escapeCell(comparison.Current.Mode)))
	b.WriteString(fmt.Sprintf("| Passed | %t | %t |\n", comparison.Base.Passed, comparison.Current.Passed))
	b.WriteString(fmt.Sprintf("| Duration | %s | %s |\n", escapeCell(emptyDash(comparison.Base.Duration)), escapeCell(emptyDash(comparison.Current.Duration))))
	b.WriteString(fmt.Sprintf("| Target Duration | %s | %s |\n", escapeCell(emptyDash(comparison.Base.TargetDuration)), escapeCell(emptyDash(comparison.Current.TargetDuration))))
	b.WriteString(fmt.Sprintf("| Rate | %s | %s |\n", formatIntCell(comparison.Base.Rate), formatIntCell(comparison.Current.Rate)))
	b.WriteString(fmt.Sprintf("| Clients | %d | %d |\n", comparison.Base.Clients, comparison.Current.Clients))
	b.WriteString(fmt.Sprintf("| Messages Per Client | %d | %d |\n", comparison.Base.MessagesPerClient, comparison.Current.MessagesPerClient))
	b.WriteString("\n")
}

func writeMetrics(b *strings.Builder, metrics []metricComparison) {
	b.WriteString("## Metrics\n\n")
	b.WriteString("| Metric | Baseline | Current | Delta | Trend |\n")
	b.WriteString("|---|---:|---:|---:|---|\n")
	for _, metric := range metrics {
		b.WriteString(fmt.Sprintf(
			"| %s | %s | %s | %s | %s |\n",
			escapeCell(metric.Name),
			formatMetricValue(metric.Kind, metric.Base, metric.BaseValid),
			formatMetricValue(metric.Kind, metric.Current, metric.CurrentValid),
			formatDelta(metric),
			metricTrend(metric),
		))
	}
}

func comparisonMetrics(comparison comparison) []metricComparison {
	baseLatency, baseLatencyOK := primaryLatency(comparison.Base)
	currentLatency, currentLatencyOK := primaryLatency(comparison.Current)
	return []metricComparison{
		{
			Name:         "QPS",
			Base:         comparison.Base.QPS,
			Current:      comparison.Current.QPS,
			BaseValid:    true,
			CurrentValid: true,
			Kind:         metricNumber,
			HigherBetter: true,
		},
		{
			Name:         "Error Rate",
			Base:         comparison.Base.ErrorRate,
			Current:      comparison.Current.ErrorRate,
			BaseValid:    true,
			CurrentValid: true,
			Kind:         metricPercent,
			HigherBetter: false,
		},
		{
			Name:         "p95",
			Base:         baseLatency.P95MS,
			Current:      currentLatency.P95MS,
			BaseValid:    baseLatencyOK,
			CurrentValid: currentLatencyOK,
			Kind:         metricMillis,
			HigherBetter: false,
		},
		{
			Name:         "p99",
			Base:         baseLatency.P99MS,
			Current:      currentLatency.P99MS,
			BaseValid:    baseLatencyOK,
			CurrentValid: currentLatencyOK,
			Kind:         metricMillis,
			HigherBetter: false,
		},
	}
}

func primaryLatency(summary loadSummary) (latencySummary, bool) {
	name := "upstream_ack"
	if summary.Mode == "downlink" {
		name = "downlink_http"
	}
	if summary.Latency == nil {
		return latencySummary{}, false
	}
	latency, ok := summary.Latency[name]
	return latency, ok && latency.Count > 0
}

func formatMetricValue(kind metricKind, value float64, valid bool) string {
	if !valid {
		return "-"
	}
	switch kind {
	case metricPercent:
		return fmt.Sprintf("%.2f%%", value*100)
	case metricMillis:
		return fmt.Sprintf("%.2fms", value)
	default:
		return fmt.Sprintf("%.2f", value)
	}
}

func formatDelta(metric metricComparison) string {
	if !metric.BaseValid || !metric.CurrentValid {
		return "-"
	}
	if almostEqual(metric.Base, 0) {
		if almostEqual(metric.Current, 0) {
			return "0.00%"
		}
		return "n/a"
	}

	delta := (metric.Current - metric.Base) / metric.Base * 100
	return fmt.Sprintf("%+.2f%%", delta)
}

func metricTrend(metric metricComparison) string {
	if !metric.BaseValid || !metric.CurrentValid {
		return "n/a"
	}
	diff := metric.Current - metric.Base
	if almostEqual(diff, 0) {
		return "same"
	}
	if almostEqual(metric.Base, 0) {
		return "n/a"
	}
	if metric.HigherBetter {
		if diff > 0 {
			return "better"
		}
		return "worse"
	}
	if diff < 0 {
		return "better"
	}
	return "worse"
}

func writeOutput(path string, content string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return os.WriteFile(path, []byte(content), 0o644)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func formatIntCell(value int) string {
	if value == 0 {
		return "-"
	}

	return fmt.Sprintf("%d", value)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}

	return value
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}

	return value
}
