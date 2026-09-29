{{/*
Copyright 2026 Rungar Authors
SPDX-License-Identifier: MIT
*/}}

{{- define "rungar.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The release's full name: the release's own if it already names the chart.
*/}}
{{- define "rungar.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "rungar.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "rungar.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "rungar.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rungar.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "rungar.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "rungar.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "rungar.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- if .Values.image.digest }}
{{- $ref = printf "%s@%s" $ref .Values.image.digest }}
{{- end }}
{{- $ref }}
{{- end }}

{{/*
The Secret holding /etc/rungar: the user's own, or the chart's.
*/}}
{{- define "rungar.secretName" -}}
{{- .Values.existingSecret | default (include "rungar.fullname" .) }}
{{- end }}

{{/*
config.yaml as the chart writes it: config or configFile, with the metrics
served where the Service reaches them when they are enabled. configFile is
left as it is, anchors and comments and all, unless the metrics need adding,
when it is parsed and written again.
*/}}
{{- define "rungar.config" -}}
{{- if .Values.configFile }}
{{- if .Values.metrics.enabled }}
{{- $config := fromYaml .Values.configFile }}
{{- if hasKey $config "Error" }}
{{- fail (printf "configFile does not parse as YAML: %s" (get $config "Error")) }}
{{- end }}
{{- include "rungar.withMetrics" (list . $config) }}
{{- else }}
{{- .Values.configFile }}
{{- end }}
{{- else }}
{{- include "rungar.withMetrics" (list . (deepCopy .Values.config)) }}
{{- end }}
{{- end }}

{{- define "rungar.withMetrics" -}}
{{- $root := index . 0 }}
{{- $config := index . 1 }}
{{- if $root.Values.metrics.enabled }}
{{- $_ := set $config "metrics" (merge (dict "enable" true "listen" (printf "0.0.0.0:%d" (int $root.Values.metrics.port))) ($config.metrics | default dict)) }}
{{- end }}
{{- toYaml $config }}
{{ end }}

{{/*
The volumes and mounts the daemon and the test share: the configuration, in
/etc/rungar, readable by the pod's group alone, as rungar warns of a secret
anyone may read.
*/}}
{{- define "rungar.configVolume" -}}
- name: config
  secret:
    secretName: {{ include "rungar.secretName" . }}
    defaultMode: 0440
{{- end }}

{{- define "rungar.configMount" -}}
- name: config
  mountPath: /etc/rungar
  readOnly: true
{{- end }}
