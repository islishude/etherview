{{- define "etherview.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "etherview.fullname" -}}
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

{{- define "etherview.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "etherview.labels" -}}
helm.sh/chart: {{ include "etherview.chart" . }}
app.kubernetes.io/name: {{ include "etherview.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "etherview.selectorLabels" -}}
app.kubernetes.io/name: {{ include "etherview.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "etherview.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "etherview.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "etherview.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "etherview.alertScopeLabels" -}}
etherview_release: {{ .Release.Name | quote }}
etherview_namespace: {{ .Release.Namespace | quote }}
chain_id: {{ .Values.config.chain.id | quote }}
{{- end }}
