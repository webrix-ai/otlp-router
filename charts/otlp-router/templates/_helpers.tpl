{{- define "otlp-router.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "otlp-router.fullname" -}}
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

{{- define "otlp-router.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "otlp-router.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "otlp-router.selectorLabels" -}}
app.kubernetes.io/name: {{ include "otlp-router.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "otlp-router.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "otlp-router.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "otlp-router.secretName" -}}
{{- default (include "otlp-router.fullname" .) .Values.auth.existingSecret }}
{{- end }}

{{- define "otlp-router.targetsConfigMap" -}}
{{- printf "%s-targets" (include "otlp-router.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Seed targets: the Willow target, when enabled, then the user's list. */}}
{{- define "otlp-router.targets" -}}
{{- $targets := list }}
{{- if .Values.willow.enabled }}
{{- $region := .Values.willow.region | default "us" }}
{{- if not (has $region (list "us" "eu" "custom")) }}{{ fail "willow.region must be us, eu or custom" }}{{ end }}
{{- $w := dict "region" $region "token" "${env:WILLOW_OTLP_TOKEN}" }}
{{- if eq $region "custom" }}
{{- if not .Values.willow.connectUrl }}{{ fail "willow.connectUrl is required when willow.region is custom" }}{{ end }}
{{- $_ := set $w "connectUrl" .Values.willow.connectUrl }}
{{- else }}
{{- if not .Values.willow.org }}{{ fail "willow.org is required when willow.enabled is true" }}{{ end }}
{{- $_ := set $w "org" .Values.willow.org }}
{{- end }}
{{- $targets = append $targets (dict "name" "Willow" "type" "willow" "enabled" true "willow" $w "filters" .Values.willow.filters) }}
{{- end }}
{{- range .Values.targets }}
{{- $targets = append $targets . }}
{{- end }}
{{- toYaml $targets }}
{{- end }}
