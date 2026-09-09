{{/* 所有 Kubernetes 资源使用稳定 selector；用户标签不能覆盖身份标签。 */}}
{{- define "linkd.fullname" -}}
{{- $name := .Values.fullnameOverride | default (printf "%s-%s" .Release.Name (.Values.nameOverride | default .Chart.Name)) -}}
{{- if gt (len $name) 24 -}}
{{- printf "%s-%s" ($name | trunc 15 | trimSuffix "-") ($name | sha256sum | trunc 8) -}}
{{- else -}}
{{- $name -}}
{{- end -}}
{{- end -}}

{{- define "linkd.deployment" -}}
{{- .Values.dispatch.deployment | default (printf "%s-%s" .Release.Namespace .Release.Name) -}}
{{- end -}}

{{- define "linkd.image" -}}
{{- $registry := .root.Values.global.imageRegistry | default .image.registry -}}
{{- $path := .image.repository -}}
{{- if $registry }}{{ $path = printf "%s/%s" $registry $path }}{{ end -}}
{{- if .image.digest -}}
{{ printf "%s@%s" $path .image.digest }}
{{- else -}}
{{ printf "%s:%s" $path (required "image.tag 或 image.digest 必须配置" .image.tag) }}
{{- end -}}
{{- end -}}

{{- define "linkd.selector" -}}
app.kubernetes.io/name: {{ .root.Values.nameOverride | default .root.Chart.Name | quote }}
app.kubernetes.io/instance: {{ .root.Release.Name | quote }}
app.kubernetes.io/component: {{ .workload.component | quote }}
{{- if .workload.group }}
linkd/worker-group: {{ .workload.group | quote }}
{{- end }}
{{- end -}}

{{- define "linkd.labels" -}}
{{- $fixed := include "linkd.selector" . | fromYaml -}}
{{- $_ := set $fixed "app.kubernetes.io/managed-by" .root.Release.Service -}}
{{- $_ := set $fixed "helm.sh/chart" (printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_") -}}
{{- mergeOverwrite (deepCopy .root.Values.commonLabels) $fixed | toYaml -}}
{{- end -}}

{{- define "linkd.workloads" -}}
{{- $name := include "linkd.fullname" . -}}
{{- $cp := .Values.controlPlane -}}
{{- $list := list (dict "name" (printf "%s-control-plane" $name) "component" "control-plane" "settings" $cp "secret" ($cp.existingSecret | default (printf "%s-control-plane" $name)) "key" $cp.secretKey "external" (not (empty $cp.existingSecret))) -}}
{{- range $group, $cluster := .Values.clusters -}}
{{- range $role := list "cleaner" "lifecycle" -}}
{{- $settings := mergeOverwrite (deepCopy (index $.Values.workerDefaults $role)) (deepCopy (index $cluster $role | default dict)) -}}
{{- $keys := $cluster.secretKeys | default dict -}}
{{- $list = append $list (dict "name" (printf "%s-%s-%s" $name $group $role) "component" $role "group" $group "cluster" $cluster "settings" $settings "labels" ($cluster.labels | default dict) "secret" ($cluster.existingSecret | default (printf "%s-%s-config" $name $group)) "key" (index $keys $role | default (printf "%s.yaml" $role)) "external" (not (empty $cluster.existingSecret))) -}}
{{- end -}}
{{- end -}}
{{- if .Values.console.enabled -}}
{{- $dt := .Values.console -}}
{{- $list = append $list (dict "name" (printf "%s-console" $name) "component" "console" "settings" $dt "secret" ($dt.existingSecret | default (printf "%s-console" $name)) "key" $dt.secretKey "external" (not (empty $dt.existingSecret))) -}}
{{- end -}}
{{- dict "items" $list | toYaml -}}
{{- end -}}

{{/* 仅调度标签按组替换；配置不得把调度作用域误写成 worker 组名。 */}}
{{- define "linkd.configuration" -}}
{{- $root := .root -}}
{{- $w := .workload -}}
{{- $config := deepCopy $root.Values.configuration -}}
{{- if $w.group }}{{ $config = mergeOverwrite $config (deepCopy ($w.cluster.configuration | default dict)) }}{{ end -}}
{{- $config = mergeOverwrite $config (deepCopy ($w.settings.configuration | default dict)) -}}
{{/* Chart 固定部署 Lifecycle；缺省时保留空对象，让配置加载器填充运行默认值。 */}}
{{- if not (hasKey $config "lifecycle") }}{{ $_ := set $config "lifecycle" dict }}{{ end -}}
{{- $dispatch := $config.dispatch | default dict -}}
{{- $_ := set $dispatch "deployment" (include "linkd.deployment" $root) -}}
{{- $_ := set $dispatch "listen" "0.0.0.0:8090" -}}
{{- $_ := set $dispatch "url" (printf "http://%s-control-plane:%v" (include "linkd.fullname" $root) $root.Values.service.port) -}}
{{- $_ := unset $dispatch "api_token" -}}
{{- $_ := unset $dispatch "worker_token" -}}
{{- $_ := set $config "dispatch" $dispatch -}}
{{- if $w.group -}}
{{- $worker := $config.worker | default dict -}}
{{- $_ := set $worker "labels" $w.labels -}}
{{- $_ := set $config "worker" $worker -}}
{{- end -}}
{{- $telemetry := $config.telemetry | default dict -}}
{{- $metrics := dict "exporter" "" -}}
{{- if $root.Values.metrics.enabled }}{{ $metrics = dict "exporter" "prometheus" "prometheus" (dict "listen_address" "0.0.0.0:9464") }}{{ end -}}
{{- $_ := set $telemetry "metrics" $metrics -}}
{{- $_ := set $config "telemetry" $telemetry -}}
{{- $_ := set $config "event_sources" list -}}
{{- $config | toYaml -}}
{{- end -}}

{{- define "linkd.validate" -}}
{{- if .Values.migrate.enabled -}}
{{- if le (int .Values.migrate.activeDeadlineSeconds) (int .Values.migrate.timeoutSeconds) }}{{ fail "migrate.activeDeadlineSeconds 必须大于 migrate.timeoutSeconds" }}{{ end -}}
{{- $reserved := list "LINKD_CONTROL_PLANE_URL" "LINKD_API_TOKEN" "LINKD_WORKER_TOKEN" "LINKD_WORKER_LABELS" "LINKD_CONFIG" -}}
{{- $settings := mergeOverwrite (deepCopy .Values.controlPlane) (deepCopy .Values.migrate) -}}
{{- $seen := dict -}}
{{- range $env := concat .Values.extraEnvVars ($settings.extraEnvVars | default list) -}}
{{- if or (has $env.name $reserved) (hasKey $seen $env.name) }}{{ fail (printf "migrate.extraEnvVars 不得覆盖保留变量或重复声明 %s" $env.name) }}{{ end -}}
{{- $_ := set $seen $env.name true -}}
{{- end -}}
{{- end -}}
{{- $_ := required "auth.existingSecret 必须配置" .Values.auth.existingSecret -}}
{{- if .Values.console.enabled -}}
{{- $_ := required "console.basicAuth.existingSecret 必须配置" .Values.console.basicAuth.existingSecret -}}
{{- end -}}

{{- if .Values.console.ingress.enabled -}}
{{- if not .Values.console.enabled }}{{ fail "Ingress 要求 console.enabled=true" }}{{ end -}}
{{- $_ := required "Ingress 要求 console.ingress.hostname" .Values.console.ingress.hostname -}}
{{- end -}}
{{- if and .Values.metrics.serviceMonitor.enabled (not .Values.metrics.enabled) }}{{ fail "ServiceMonitor 要求 metrics.enabled=true" }}{{ end -}}
{{- range $w := (include "linkd.workloads" . | fromYaml).items -}}
{{- $reserved := list "LINKD_CONTROL_PLANE_URL" "LINKD_API_TOKEN" "LINKD_WORKER_TOKEN" "LINKD_WORKER_LABELS" "LINKD_CONFIG" "LINKD_CONSOLE_MODE" "LINKD_CONSOLE_HOST" "LINKD_CONSOLE_PORT" "LINKD_CONSOLE_BASIC_AUTH_ENABLED" "LINKD_CONSOLE_BASIC_AUTH_USERNAME" "LINKD_CONSOLE_BASIC_AUTH_PASSWORD" "LINKD_CONSOLE_PROMETHEUS_URL" -}}
{{- $seen := dict -}}
{{- range $env := concat $.Values.extraEnvVars ($w.settings.extraEnvVars | default list) -}}
{{- if or (has $env.name $reserved) (hasKey $seen $env.name) }}{{ fail (printf "%s: extraEnvVars 不得覆盖保留变量或重复声明 %s" $w.name $env.name) }}{{ end -}}
{{- $_ := set $seen $env.name true -}}
{{- end -}}
{{- if $w.external -}}
{{- if or (not (empty $.Values.configuration)) (not (empty $w.settings.configuration)) (and $w.group (not (empty $w.cluster.configuration))) -}}
{{- fail (printf "%s: existingSecret 与适用的内联 configuration 互斥" $w.name) -}}
{{- end -}}
{{- else -}}
{{- $cfg := include "linkd.configuration" (dict "root" $ "workload" $w) | fromYaml -}}
{{- if not $cfg.storage }}{{ fail (printf "%s: 必须提供 configuration.storage 或 existingSecret" $w.name) }}{{ end -}}
{{- end -}}
{{- end -}}
{{- end -}}
