# V7 Docker Image Release Plan

V7 is the planning track for the next public milestone after `v0.6.0`. Its
target SemVer version is `v0.7.0`, not `v7.0.0`.

The goal is to make the gateway image itself a first-class release artifact, so
users can deploy Z-Courier from GitHub Container Registry without building the
image locally.

## Scope

The first `v0.7.0` image release milestone includes:

- A `Release Docker Image` GitHub Actions workflow.
- Release-triggered publishing to
  `ghcr.io/qiuyier/z-courier-gateway:<release-tag>`.
- Multi-architecture image manifests for `linux/amd64` and `linux/arm64`.
- Manual `workflow_dispatch` support for publishing an existing tag.
- Container smoke checks before pushing the image.
- Optional `latest` publishing for stable releases only.
- Helm default image repository set to the official GHCR gateway image.
- Helm chart version bump for the repository default change.
- Documentation for release behavior, manual backfill, and production use.

## Image Tags

The release workflow always pushes the immutable release tag:

```text
ghcr.io/qiuyier/z-courier-gateway:v0.7.0
```

For a normal GitHub Release, the workflow also pushes:

```text
ghcr.io/qiuyier/z-courier-gateway:latest
```

`latest` is not pushed for pre-release tags such as `v0.7.0-rc.1`. Manual runs
can opt into `latest`, but the default is false.

## Platforms

Published gateway images are multi-architecture manifests:

```text
linux/amd64
linux/arm64
```

This lets x86 Linux servers, ARM Linux servers, and Apple Silicon development
machines pull the same tag while Docker selects the native platform image.

## Manual Backfill

Because this workflow is added after `v0.6.0` was already released, it will not
automatically publish or upgrade the `v0.6.0` image unless the workflow is run
manually.

To backfill or replace an existing tag with a multi-arch manifest after this
workflow lands on `main`:

```bash
gh workflow run release-docker-image.yml \
  --ref main \
  -f tag=v0.6.0 \
  -f push_latest=false
```

Then confirm:

```bash
docker pull ghcr.io/qiuyier/z-courier-gateway:v0.6.0
docker buildx imagetools inspect ghcr.io/qiuyier/z-courier-gateway:v0.6.0
docker run --rm --entrypoint /bin/sh ghcr.io/qiuyier/z-courier-gateway:v0.6.0 -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
```

If the package appears private on its first publish, make the package public in
GitHub Packages before documenting it as a public install path.

## Helm Defaults

The chart default image repository is:

```yaml
image:
  repository: ghcr.io/qiuyier/z-courier-gateway
  tag: ""
```

When `image.tag` is empty, the chart uses `Chart.yaml` `appVersion`.

Because changing the default repository changes the packaged chart behavior,
the next chart package version is `0.2.0`. At `v0.7.0` release time, confirm
that `appVersion` is updated to `"v0.7.0"` before tagging.

## Verification

Run these checks before merging image release changes:

```bash
actionlint
helm lint deploy/helm/z-courier
helm lint deploy/helm/z-courier -f deploy/helm/z-courier/examples/values-production.yaml
helm template z-courier deploy/helm/z-courier >/tmp/z-courier-k8s.yaml
DOCKER_BUILDKIT=1 docker build --tag z-courier-gateway:release-image-check .
docker run --rm --entrypoint /bin/sh z-courier-gateway:release-image-check -c \
  'test -x /usr/local/bin/z-courier-gateway && test -f /app/configs/z-courier.yaml && test -f /app/conf/zinx.json'
```

After publishing a release, confirm:

```bash
docker pull ghcr.io/qiuyier/z-courier-gateway:<release-tag>
docker buildx imagetools inspect ghcr.io/qiuyier/z-courier-gateway:<release-tag>
helm template z-courier oci://ghcr.io/qiuyier/charts/z-courier \
  --version <chart-version> \
  --set image.tag=<release-tag>
```

## Boundaries

This milestone publishes the gateway image only. It does not publish database,
Redis, NSQ, Prometheus, Grafana, auth backend, or business backend images.
