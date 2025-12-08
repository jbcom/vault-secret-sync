{{/*
Expand the name of the chart.
*/}}
{{- define "secretsync-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "secretsync-operator.fullname" -}}
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
{{- define "secretsync-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "secretsync-operator.labels" -}}
helm.sh/chart: {{ include "secretsync-operator.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Common Selector labels
*/}}
{{- define "secretsync-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "secretsync-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Operator Selector labels
*/}}
{{- define "secretsync-operator.operatorLabels" -}}
{{ include "secretsync-operator.labels" . }}
{{ include "secretsync-operator.selectorLabels" . }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "secretsync-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "secretsync-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}


{{/*
Simplify the definition of the kube metrics port
*/}}
{{- define "secretsync-operator.kubeMetricsPort" -}}
{{- if .Values.config.backend }}
{{- default "9080" .Values.config.backend.metricsAddr }}
{{- else }}
{{- "9080" }}
{{- end }}
{{- end }}

{{/*
Simplify the definition of the event port
*/}}
{{- define "secretsync-operator.metricsPort" -}}
{{- default "9090" .Values.metricsPort }}
{{- end }}

{{/*
Create the name of the configMap to use
*/}}
{{- define "secretsync-operator.configMapName" -}}
{{- if .Values.existingConfigMap }}
{{- .Values.existingConfigMap -}}
{{- else }}
{{- include "secretsync-operator.fullname" . -}}
{{- end }}
{{- end -}}