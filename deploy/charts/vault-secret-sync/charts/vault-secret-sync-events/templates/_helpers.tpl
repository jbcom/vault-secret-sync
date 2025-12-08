{{/*
Expand the name of the chart.
*/}}
{{- define "secretsync-events.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "secretsync-events.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "secretsync-events.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "secretsync-events.labels" -}}
helm.sh/chart: {{ include "secretsync-events.chart" . }}
{{ include "secretsync-events.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "secretsync-events.selectorLabels" -}}
app.kubernetes.io/name: {{ include "secretsync-events.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "secretsync-events.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "secretsync-events.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Simplify the definition of the event port
*/}}
{{- define "secretsync-events.containerPort" -}}
{{- default "8080" .Values.containerPort }}
{{- end }}

{{/*
Simplify the definition of the event port
*/}}
{{- define "secretsync-events.metricsPort" -}}
{{- default "9090" .Values.metricsPort }}
{{- end }}


{{/*
Create the name of the configMap to use
*/}}
{{- define "secretsync-events.configMapName" -}}
{{- if .Values.existingConfigMap }}
{{- .Values.existingConfigMap -}}
{{- else }}
{{- include "secretsync-events.fullname" . -}}
{{- end }}
{{- end -}}