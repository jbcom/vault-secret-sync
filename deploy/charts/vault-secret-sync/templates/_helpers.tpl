{{/*
Expand the name of the chart.
*/}}
{{- define "secretsync.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}


{{/*
Define the name of the configMap.
*/}}
{{- define "secretsync.configMapName" -}}
{{- if .Values.existingConfigMap }}
{{- .Values.existingConfigMap -}}
{{- else if .Values.configMapName }}
{{- .Values.configMapName -}}
{{- else }}
{{- printf "%s-%s" .Chart.Name "config" | trunc 63 | trimSuffix "-" -}}
{{- end }}
{{- end -}}


{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "secretsync.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "secretsync.labels" -}}
helm.sh/chart: {{ include "secretsync.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}