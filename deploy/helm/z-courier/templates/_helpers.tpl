{{/*
Expand the name of the chart.
*/}}
{{- define "z-courier.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "z-courier.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by chart labels.
*/}}
{{- define "z-courier.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "z-courier.labels" -}}
helm.sh/chart: {{ include "z-courier.chart" . }}
{{ include "z-courier.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "z-courier.selectorLabels" -}}
app.kubernetes.io/name: {{ include "z-courier.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Service account name.
*/}}
{{- define "z-courier.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "z-courier.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Secret name.
*/}}
{{- define "z-courier.secretName" -}}
{{- default (printf "%s-secret" (include "z-courier.fullname" .)) .Values.secret.name -}}
{{- end -}}

{{/*
Gateway image.
*/}}
{{- define "z-courier.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Headless service name used for per-pod peer push DNS.
*/}}
{{- define "z-courier.headlessServiceName" -}}
{{- printf "%s-headless" (include "z-courier.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Per-pod internal URL template resolved by Z-Courier env expansion at runtime.
*/}}
{{- define "z-courier.podInternalAddr" -}}
{{- printf "http://${POD_NAME}.%s.${POD_NAMESPACE}.svc.cluster.local:%v" (include "z-courier.headlessServiceName" .) .Values.internalHttp.port -}}
{{- end -}}

