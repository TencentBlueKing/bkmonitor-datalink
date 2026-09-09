{{- define "linkd.migrateName" -}}
{{- if .Values.migrate.watch -}}
{{- printf "%s-migrate" (include "linkd.fullname" .) -}}
{{- else -}}
{{- printf "%s-migrate-r%v" (include "linkd.fullname" .) .Release.Revision -}}
{{- end -}}
{{- end -}}
