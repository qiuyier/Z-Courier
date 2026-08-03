{{/* Render upstream routes with the gateway route-document field names. */}}
{{- define "z-courier.upstreamRoutes" -}}
{{- if .Values.upstream.routes }}
{{- range .Values.upstream.routes }}
- name: {{ .name | quote }}
  enabled: {{ .enabled }}
  msg_id_min: {{ .msgIDMin }}
  msg_id_max: {{ .msgIDMax }}
  target:
    type: {{ .target.type | quote }}
    {{- if eq .target.type "http" }}
    {{- with .target.url }}
    url: {{ . | quote }}
    {{- end }}
    {{- with .target.path }}
    path: {{ . | quote }}
    {{- end }}
    token: {{ printf "${%s}" .target.tokenEnv | quote }}
    timeout: {{ .target.timeout | quote }}
    max_in_flight: {{ .target.maxInFlight }}
    {{- with .target.discovery }}
    discovery:
      type: {{ .type | quote }}
      {{- if eq .type "static" }}
      endpoints:
        {{- toYaml .endpoints | nindent 8 }}
      {{- else if eq .type "dns" }}
      scheme: {{ .scheme | quote }}
      hostname: {{ .hostname | quote }}
      port: {{ .port }}
      {{- with .refreshInterval }}
      refresh_interval: {{ . | quote }}
      {{- end }}
      {{- end }}
    {{- end }}
    {{- with .target.failover }}
    failover:
      enabled: {{ .enabled }}
      {{- with .maxAttempts }}
      max_attempts: {{ . }}
      {{- end }}
      {{- with .unhealthyCooldown }}
      unhealthy_cooldown: {{ . | quote }}
      {{- end }}
    {{- end }}
    {{- else if eq .target.type "nsq" }}
    nsqd_addrs:
      {{- toYaml .target.nsqdAddrs | nindent 6 }}
    topic: {{ .target.topic | quote }}
    auth_secret: {{ .target.authSecret | quote }}
    dial_timeout: {{ .target.dialTimeout | quote }}
    read_timeout: {{ .target.readTimeout | quote }}
    write_timeout: {{ .target.writeTimeout | quote }}
    publish_mode: {{ .target.publishMode | quote }}
    retry_attempts: {{ .target.retryAttempts }}
    max_in_flight: {{ .target.maxInFlight }}
    {{- end }}
{{- end }}
{{- else }}
[]
{{- end }}
{{- end }}
