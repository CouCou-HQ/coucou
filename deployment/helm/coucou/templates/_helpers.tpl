{{- define "coucou.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "coucou.fullname" -}}
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

{{- define "coucou.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "coucou.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "coucou.selectorLabels" -}}
app.kubernetes.io/name: {{ include "coucou.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Both credentials resolve the same way: an existing Secret if one is named, otherwise the Secret
this chart creates. Failing loudly beats deploying a bot that cannot log in.
*/}}
{{- define "coucou.discordSecretName" -}}
{{- if .Values.discord.existingSecret -}}
{{- .Values.discord.existingSecret -}}
{{- else if .Values.discord.token -}}
{{- include "coucou.fullname" . -}}
{{- else -}}
{{- fail "set discord.existingSecret or discord.token — the bot cannot start without a token" -}}
{{- end -}}
{{- end -}}

{{- define "coucou.discordSecretKey" -}}
{{- if .Values.discord.existingSecret -}}{{ .Values.discord.existingSecretKey }}{{- else -}}discord-token{{- end -}}
{{- end -}}

{{- define "coucou.databaseSecretName" -}}
{{- if .Values.database.existingSecret -}}
{{- .Values.database.existingSecret -}}
{{- else if .Values.database.url -}}
{{- include "coucou.fullname" . -}}
{{- else -}}
{{- fail "set database.existingSecret or database.url — this chart brings no database of its own" -}}
{{- end -}}
{{- end -}}

{{- define "coucou.databaseSecretKey" -}}
{{- if .Values.database.existingSecret -}}{{ .Values.database.existingSecretKey }}{{- else -}}database-url{{- end -}}
{{- end -}}
