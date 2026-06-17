# Load Test Baselines

This directory is reserved for optional load-test baseline reports used by
GitHub Actions summaries.

Preferred filenames:

```text
loadtest-smoke/upstream.json
loadtest-smoke/downlink.json
loadtest-manual/upstream.json
loadtest-manual/downlink.json
```

Keep smoke and manual baselines separate. Smoke reports are intentionally small
CI acceptance checks. Manual baselines are intended for more meaningful
sustained runs such as 60 seconds at a fixed target rate.

For compatibility, workflows also fall back to:

```text
upstream.json
downlink.json
```

Create a smoke baseline from a known-good report:

```bash
mkdir -p reports/baseline/loadtest-smoke
cp reports/loadtest-smoke/downlink.json reports/baseline/loadtest-smoke/downlink.json
```

Create a manual load-test baseline from a known-good report:

```bash
mkdir -p reports/baseline/loadtest-manual
cp reports/loadtest-manual/downlink.json reports/baseline/loadtest-manual/downlink.json
```

When a matching baseline exists, CI and the manual load-test workflow generate a
Markdown comparison with `cmd/loadcompare` and append it to the workflow summary.
Comparison output is informational only and does not fail the workflow.

The current manual baselines were generated with:

```text
duration: 60s
rate: 100
clients: 100
body_size: 128
downlink_http_concurrency: 50
```
