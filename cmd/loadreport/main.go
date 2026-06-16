package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bytedance/sonic"
)

type config struct {
	OutputPath string
	Inputs     []string
}

type reportFile struct {
	Path    string
	Summary loadSummary
}

type loadSummary struct {
	Mode              string                    `json:"mode"`
	Clients           int                       `json:"clients"`
	MessagesPerClient int                       `json:"messages_per_client"`
	HTTPConcurrency   int                       `json:"http_concurrency,omitempty"`
	Duration          string                    `json:"duration"`
	TargetDuration    string                    `json:"target_duration,omitempty"`
	Rate              int                       `json:"rate,omitempty"`
	QPS               float64                   `json:"qps"`
	ErrorRate         float64                   `json:"error_rate"`
	Counts            summaryCounts             `json:"counts"`
	Latency           map[string]latencySummary `json:"latency,omitempty"`
	Failures          map[string]int64          `json:"failures,omitempty"`
	Checks            []thresholdCheck          `json:"checks,omitempty"`
	Passed            bool                      `json:"passed"`
	Error             string                    `json:"error,omitempty"`
}

type summaryCounts struct {
	BindAccepted     int64 `json:"bind_accepted"`
	BindRejected     int64 `json:"bind_rejected"`
	UpstreamAccepted int64 `json:"upstream_accepted"`
	UpstreamRejected int64 `json:"upstream_rejected"`
	DownlinkSuccess  int64 `json:"downlink_success"`
	DownlinkRejected int64 `json:"downlink_rejected"`
	SendErrors       int64 `json:"send_errors"`
	DecodeErrors     int64 `json:"decode_errors"`
}

type latencySummary struct {
	Count int     `json:"count"`
	Min   string  `json:"min"`
	Avg   string  `json:"avg"`
	P50   string  `json:"p50"`
	P95   string  `json:"p95"`
	P99   string  `json:"p99"`
	Max   string  `json:"max"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

type thresholdCheck struct {
	Name     string  `json:"name"`
	Passed   bool    `json:"passed"`
	Actual   float64 `json:"actual"`
	Expected float64 `json:"expected"`
	Operator string  `json:"operator"`
	Reason   string  `json:"reason,omitempty"`
}

func main() {
	cfg := parseFlags()
	reports, err := readReports(cfg.Inputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadreport failed: %v\n", err)
		os.Exit(1)
	}

	markdown := renderMarkdown(reports)
	if cfg.OutputPath == "" {
		fmt.Print(markdown)
		return
	}
	if err := writeOutput(cfg.OutputPath, markdown); err != nil {
		fmt.Fprintf(os.Stderr, "write report summary failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	fs := flag.NewFlagSet("loadreport", flag.ExitOnError)
	fs.StringVar(&cfg.OutputPath, "output", "", "write Markdown summary to this file; stdout when empty")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: loadreport [flags] report.json [report.json...]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg.Inputs = fs.Args()
	if len(cfg.Inputs) == 0 {
		fs.Usage()
		os.Exit(2)
	}

	return cfg
}

func readReports(inputs []string) ([]reportFile, error) {
	paths, err := expandInputs(inputs)
	if err != nil {
		return nil, err
	}

	reports := make([]reportFile, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		var summary loadSummary
		if err := sonic.Unmarshal(data, &summary); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		reports = append(reports, reportFile{Path: path, Summary: summary})
	}

	return reports, nil
}

func expandInputs(inputs []string) ([]string, error) {
	seen := make(map[string]struct{})
	var paths []string
	for _, input := range inputs {
		matches := []string{input}
		if hasGlobMeta(input) {
			globMatches, err := filepath.Glob(input)
			if err != nil {
				return nil, fmt.Errorf("expand %s: %w", input, err)
			}
			matches = globMatches
		}
		for _, match := range matches {
			if _, ok := seen[match]; ok {
				continue
			}
			seen[match] = struct{}{}
			paths = append(paths, match)
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no report files matched")
	}

	return paths, nil
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func renderMarkdown(reports []reportFile) string {
	var b strings.Builder
	b.WriteString("# Load Test Report\n\n")
	b.WriteString("| Report | Mode | Passed | QPS | Error Rate | p95 | p99 | Duration | Target | Rate | Clients | Messages |\n")
	b.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, report := range reports {
		summary := report.Summary
		latency := primaryLatency(summary)
		b.WriteString(fmt.Sprintf(
			"| %s | %s | %t | %.2f | %.2f%% | %s | %s | %s | %s | %s | %d | %d |\n",
			escapeCell(reportLabel(report.Path)),
			escapeCell(summary.Mode),
			summary.Passed,
			summary.QPS,
			summary.ErrorRate*100,
			formatLatencyCell(latency.P95),
			formatLatencyCell(latency.P99),
			escapeCell(emptyDash(summary.Duration)),
			escapeCell(emptyDash(summary.TargetDuration)),
			formatIntCell(summary.Rate),
			summary.Clients,
			summary.MessagesPerClient,
		))
	}

	b.WriteString("\n## Checks\n\n")
	for _, report := range reports {
		writeChecks(&b, report)
	}

	b.WriteString("\n## Counts\n\n")
	b.WriteString("| Report | Bind Accepted | Bind Rejected | Upstream Accepted | Upstream Rejected | Downlink Success | Downlink Rejected | Send Errors | Decode Errors |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, report := range reports {
		counts := report.Summary.Counts
		b.WriteString(fmt.Sprintf(
			"| %s | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			escapeCell(reportLabel(report.Path)),
			counts.BindAccepted,
			counts.BindRejected,
			counts.UpstreamAccepted,
			counts.UpstreamRejected,
			counts.DownlinkSuccess,
			counts.DownlinkRejected,
			counts.SendErrors,
			counts.DecodeErrors,
		))
	}

	writeFailures(&b, reports)
	writeErrors(&b, reports)

	return b.String()
}

func writeChecks(b *strings.Builder, report reportFile) {
	name := reportLabel(report.Path)
	if len(report.Summary.Checks) == 0 {
		b.WriteString(fmt.Sprintf("### %s\n\nNo threshold checks were configured.\n\n", escapeHeading(name)))
		return
	}

	b.WriteString(fmt.Sprintf("### %s\n\n", escapeHeading(name)))
	b.WriteString("| Check | Passed | Actual | Expected | Reason |\n")
	b.WriteString("|---|---:|---:|---:|---|\n")
	for _, check := range report.Summary.Checks {
		b.WriteString(fmt.Sprintf(
			"| %s | %t | %.4f | %s %.4f | %s |\n",
			escapeCell(check.Name),
			check.Passed,
			check.Actual,
			escapeCell(check.Operator),
			check.Expected,
			escapeCell(emptyDash(check.Reason)),
		))
	}
	b.WriteString("\n")
}

func writeFailures(b *strings.Builder, reports []reportFile) {
	hasFailures := false
	for _, report := range reports {
		if len(report.Summary.Failures) > 0 {
			hasFailures = true
			break
		}
	}
	if !hasFailures {
		return
	}

	b.WriteString("\n## Failure Reasons\n\n")
	b.WriteString("| Report | Reason | Count |\n")
	b.WriteString("|---|---|---:|\n")
	for _, report := range reports {
		reasons := sortedKeys(report.Summary.Failures)
		for _, reason := range reasons {
			b.WriteString(fmt.Sprintf("| %s | %s | %d |\n", escapeCell(reportLabel(report.Path)), escapeCell(reason), report.Summary.Failures[reason]))
		}
	}
}

func writeErrors(b *strings.Builder, reports []reportFile) {
	var errored []reportFile
	for _, report := range reports {
		if strings.TrimSpace(report.Summary.Error) != "" {
			errored = append(errored, report)
		}
	}
	if len(errored) == 0 {
		return
	}

	b.WriteString("\n## Run Errors\n\n")
	b.WriteString("| Report | Error |\n")
	b.WriteString("|---|---|\n")
	for _, report := range errored {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", escapeCell(reportLabel(report.Path)), escapeCell(report.Summary.Error)))
	}
}

func primaryLatency(summary loadSummary) latencySummary {
	name := "upstream_ack"
	if summary.Mode == "downlink" {
		name = "downlink_http"
	}
	if summary.Latency == nil {
		return latencySummary{}
	}

	return summary.Latency[name]
}

func writeOutput(path string, content string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return os.WriteFile(path, []byte(content), 0o644)
}

func sortedKeys(items map[string]int64) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func reportLabel(path string) string {
	return filepath.ToSlash(path)
}

func formatLatencyCell(value string) string {
	if value == "" {
		return "-"
	}

	return escapeCell(value)
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

func escapeHeading(value string) string {
	return strings.ReplaceAll(value, "\n", " ")
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}

	return value
}
