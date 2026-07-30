#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_FILE="$ROOT_DIR/deploy/helm/z-courier/Chart.yaml"
PRODUCTION_VALUES_FILE="$ROOT_DIR/deploy/helm/z-courier/examples/values-production.yaml"
EXPECTED_APP_VERSION="${1:-}"

if [[ $# -gt 1 ]]; then
  echo "usage: $0 [expected-app-version]" >&2
  exit 2
fi

fail() {
  echo "helm release metadata check failed: $*" >&2
  exit 1
}

read_chart_scalar() {
  local key="$1"

  awk -v key="$key" '
    $0 ~ ("^" key ":[[:space:]]*") {
      value = $0
      sub("^[^:]+:[[:space:]]*", "", value)
      gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", value)
      print value
      found = 1
      exit
    }
    END {
      if (!found) {
        exit 1
      }
    }
  ' "$CHART_FILE"
}

read_production_image_tag() {
  awk '
    /^image:[[:space:]]*$/ {
      in_image = 1
      next
    }
    in_image && /^[^[:space:]]/ {
      exit
    }
    in_image && /^[[:space:]]+tag:[[:space:]]*/ {
      value = $0
      sub(/^[[:space:]]+tag:[[:space:]]*/, "", value)
      gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", value)
      print value
      found = 1
      exit
    }
    END {
      if (!found) {
        exit 1
      }
    }
  ' "$PRODUCTION_VALUES_FILE"
}

chart_version="$(read_chart_scalar version)" ||
  fail "could not read version from $CHART_FILE"
app_version="$(read_chart_scalar appVersion)" ||
  fail "could not read appVersion from $CHART_FILE"
production_image_tag="$(read_production_image_tag)" ||
  fail "could not read image.tag from $PRODUCTION_VALUES_FILE"

if [[ ! "$chart_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  fail "Chart version is not SemVer: $chart_version"
fi

if [[ ! "$app_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  fail "Chart appVersion is not a gateway release tag: $app_version"
fi

if [[ "$production_image_tag" != "$app_version" ]]; then
  fail "production image tag $production_image_tag does not match Chart appVersion $app_version"
fi

if [[ -n "$EXPECTED_APP_VERSION" && "$app_version" != "$EXPECTED_APP_VERSION" ]]; then
  fail "Chart appVersion $app_version does not match release tag $EXPECTED_APP_VERSION"
fi

echo "helm release metadata check passed: chart=$chart_version app=$app_version"
