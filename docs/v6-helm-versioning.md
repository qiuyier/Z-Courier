# V6 Helm Versioning

This document defines how Z-Courier publishes and versions the Kubernetes Helm
chart introduced in the `v0.6.0` track.

The important rule is that the Helm chart has its own package version. It is
related to, but not the same as, the Z-Courier Git release tag or gateway image
tag.

## Version Fields

`deploy/helm/z-courier/Chart.yaml` owns two version fields:

| Field | Example | Meaning |
| --- | --- | --- |
| `version` | `0.1.0` | Helm chart package version used by `.tgz`, GitHub Release assets, and GHCR OCI installs. |
| `appVersion` | `v0.6.0` | Recommended Z-Courier gateway application/image version for this chart. |

Helm clients select charts by `version`:

```bash
helm upgrade --install z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version 0.1.0 \
  --namespace z-courier \
  -f values-production.yaml
```

The gateway container image tag defaults to `appVersion` when
`image.tag` is empty:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: ""
```

Operators can override `image.tag` when they intentionally run another gateway
build with the same chart.

## Chart Version Policy

The chart follows SemVer independently from the gateway application version.

| Change type | Chart bump | Examples |
| --- | --- | --- |
| Patch | `0.1.0 -> 0.1.1` | Documentation-only chart release, comments, non-behavioral template cleanup, test-only example updates. |
| Minor | `0.1.0 -> 0.2.0` | New optional values, new optional templates, new examples, new metadata that keeps existing values compatible. |
| Major | `0.x -> 1.0.0` after chart maturity | Breaking values changes, renamed templates, changed selectors, changed resource ownership, or incompatible default behavior. |

While Z-Courier itself is still pre-`1.0`, chart `0.x` releases may still carry
larger changes than a post-`1.0` chart would. Breaking changes must be called
out in release notes and migration docs.

Always bump `Chart.yaml` `version` before publishing a changed chart package.
GitHub Release assets and GHCR OCI packages are immutable enough in practice
that reusing the same chart version will confuse users and may fail during
upload or pull.

## App Version Policy

Set `appVersion` to the recommended gateway image tag for the chart.

For the `v0.6.0` release, the expected release-time update is:

```yaml
version: 0.1.0
appVersion: "v0.6.0"
```

If the chart itself changes after `0.1.0` is published but still targets the
same gateway release, bump only the chart version:

```yaml
version: 0.1.1
appVersion: "v0.6.0"
```

If a later gateway release is validated with the same chart behavior, bump
`appVersion` and decide the chart bump by the chart change itself.

## Compatibility Matrix

Maintain this matrix when releasing or changing the chart:

| Chart version | Recommended appVersion | Gateway image tag | Protocol/config compatibility | Notes |
| --- | --- | --- | --- | --- |
| `0.1.0` | `v0.6.0` at release time | `v0.6.0` | Gateway config rendered by this chart must pass `values.schema.json`, kind smoke, and kind E2E. | First Kubernetes/Helm chart release. |
| `0.2.0` | `v0.7.0` | `v0.7.0` | Same protocol and config contract as `0.1.0`; chart renders the official GHCR gateway image repository by default. | First chart update after adding gateway image publishing. |
| `0.3.0` | `v0.8.1` | `v0.8.1` | Same protocol and config contract as `0.2.0`; chart defaults to the v0.8 production diagnostics gateway image. | Hotfix chart release aligning default image metadata after `v0.8.0`. |
| `0.4.0` | `v0.9.0` | `v0.9.0` | Same protocol and config contract as `0.3.0`; chart exposes optional admin console values while keeping the console disabled by default. | Chart release aligned with the embedded Web admin console gateway image. |
| `0.4.1` | `v0.9.1` | `v0.9.1` | Same protocol, config contract, and chart behavior as `0.4.0`. | Patch chart release aligned with the Chinese documentation refresh. |
| `0.5.0` | `v0.10.0` | `v0.10.0` | Same packet protocol and dependency contract as `0.4.1`; chart metadata and production examples align with the V10 admin console operations release. | Chart release aligned with guarded console operations, browser sessions, and role-aware admin workflows. |
| `0.6.0` | `v0.11.0` | `v0.11.0` | Same packet protocol and dependency contract as `0.5.0`; chart values support Redis admin sessions and PostgreSQL admin audit storage. | Chart release aligned with the durable, cluster-aware V11 admin control plane. |
| `0.7.0` | `v0.12.0` | `v0.12.0` | Same packet protocol and dependency contract as `0.6.0`; chart values add optional MsgID delivery policies and NSQ terminal-event publication while preserving empty/`none` defaults. | Chart release aligned with the V12 reliable-downlink lifecycle and policy-exhaustion Kubernetes E2E. |
| `0.8.0` | `v0.16.0` | `v0.16.0` | Same packet protocol and dependency contract as `0.7.0`; chart values add terminal HTTP TLS/mTLS, HMAC rotation, HTTP upstream discovery, and optional local/Redis named traffic policies while preserving disabled and legacy-compatible defaults. | Chart release aligned with the V16 traffic-policy production and release closure. |

If the chart is used with a gateway image that differs from `appVersion`, the
operator owns compatibility validation. The safest check is:

```bash
bash scripts/k8s_helm_smoke.sh
bash scripts/k8s_helm_e2e.sh
```

## Release Checklist

Before publishing a release that includes Helm chart changes:

1. Decide whether the chart `version` must change.
2. Set `appVersion` to the intended gateway image tag.
3. Update this compatibility matrix if the published chart version changes.
4. Run local validation:

   ```bash
   go test ./...
   actionlint
   bash -n scripts/*.sh
   helm lint deploy/helm/z-courier
   helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
   helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-kind-smoke.yaml
   helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-k8s-e2e.yaml
   bash scripts/k8s_helm_smoke.sh
   bash scripts/k8s_helm_e2e.sh
   ```

5. Confirm CI is green on `main`.
6. Publish the GitHub Release.
7. Confirm release assets include:
   - `z-courier-<chart-version>.tgz`
   - `SHA256SUMS`
8. Confirm GHCR OCI install works:

   ```bash
   helm pull oci://ghcr.io/qiuyier/charts/z-courier --version <chart-version>
   ```

## Operational Guidance

Production operators should pin both the chart version and gateway image tag:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: v0.16.0
```

This makes rollbacks explicit:

```bash
helm rollback z-courier <revision> --namespace z-courier
```

If a chart rollback also requires an image rollback, update `image.tag` in the
values file and run `helm upgrade` with the older chart version.
