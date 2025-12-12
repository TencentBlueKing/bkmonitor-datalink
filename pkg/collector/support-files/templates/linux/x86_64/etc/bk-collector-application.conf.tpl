type: 'subconfig'
token: '{{ bk_data_token }}'
bk_biz_id: {{ bk_biz_id }}
bk_app_name: {{ bk_app_name }}
traces_dataid: {{ trace_data_id | default(0) }}
metrics_dataid: {{ metric_data_id | default(0) }}
logs_dataid: {{ log_data_id | default(0) }}
profiles_dataid: {{ profile_data_id | default(0) }}

{% if sdk_config is defined %}
skywalking_agent:
  sn: "{{ sdk_config.sn }}"
  rules:
    {%- for rule in sdk_config.rules %}
    - type: "{{ rule.type }}"
      enabled: {{ rule.enabled }}
      target: "{{ rule.target }}"
      field: "{{ rule.field }}"
    {%- endfor %}
{%- endif %}

{% if queue_config is defined %}
exporter:
  queue:
    {% if queue_config.logs_batch_size is defined %}
    logs_batch_size: {{ queue_config.logs_batch_size }}
    {%- endif %}
    {% if queue_config.metrics_batch_size is defined %}
    metrics_batch_size: {{ queue_config.metrics_batch_size }}
    {%- endif %}
    {% if queue_config.traces_batch_size is defined %}
    traces_batch_size: {{ queue_config.traces_batch_size }}
    {%- endif %}
    {% if queue_config.profiles_batch_size is defined %}
    profiles_batch_size: {{ queue_config.profiles_batch_size }}
    {%- endif %}
{%- endif %}

default:
  processor:
{% if apdex_config is defined %}
      - name: '{{ apdex_config.name }}'
        config:
          calculator:
            type: '{{ apdex_config.type }}'
          rules:
            {%- for rule_config in apdex_config.rules %}
            - kind: '{{ rule_config.kind }}'
              predicate_key: '{{ rule_config.predicate_key }}'
              metric_name: '{{ rule_config.metric_name }}'
              destination: '{{ rule_config.destination }}'
              apdex_t: {{ rule_config.apdex_t }} # ms
            {%- endfor %}
{%- endif %}

{% if token_checker is defined %}
      - name: "{{ token_checker.name }}"
        config:
          profiles_data_id: {{ token_checker.profiles_data_id }}
{%- endif %}

{% if license_config is defined %}
      - name: "{{ license_config.name }}"
        config:
          enabled: {{ license_config.enabled }}
          expire_time: {{ license_config.expire_time }}
          tolerable_expire: {{ license_config.tolerable_expire }}
          number_nodes: {{ license_config.number_nodes }}
          tolerable_num_ratio: {{ license_config.tolerable_num_ratio }}
{%- endif %}

{% if traces_drop_sampler_config is defined %}
      - name: "{{ traces_drop_sampler_config.name }}"
        config:
          type: "{{ traces_drop_sampler_config.type }}"
          enabled: {{ traces_drop_sampler_config.enabled }}
{%- endif %}

{% if profiles_drop_sampler_config is defined %}
      - name: "{{ profiles_drop_sampler_config.name }}"
        config:
          type: "{{ profiles_drop_sampler_config.type }}"
          enabled: {{ profiles_drop_sampler_config.enabled }}
{%- endif %}

{% if metrics_filter_config is defined %}
      - name: "{{ metrics_filter_config.name }}"
        config:
          code_relabel:
            {%- for item in metrics_filter_config.code_relabel %}
            - metrics: {{ item.metrics | tojson }}
              source: "{{ item.source }}"
              services:
              {%- for svc in item.services %}
              - name: "{{ svc.name }}"
                codes:
                {%- for c in svc.codes %}
                - rule: "{{ c.rule }}"
                  target:
                    action: "{{ c.target.action }}"
                    label: "{{ c.target.label }}"
                    value: "{{ c.target.value }}"
                {%- endfor %}
              {%- endfor %}
            {%- endfor %}
{%- endif %}

{% if db_slow_command_config is defined %}
      - name: "{{ db_slow_command_config.name }}"
        config:
          slow_query:
            destination: "{{db_slow_command_config.destination}}"
            rules:
              {%- for rule in db_slow_command_config.rules %}
              - match: "{{ rule.match }}"
                threshold: {{ rule.threshold }}ms
              {%- endfor %}
{%- endif %}

{% if sdk_config_scope is defined %}
      # sdk config scope
      - name: "{{ sdk_config_scope.name }}"
        config:
          add_attributes:
            - rules:
                {%- for rule in sdk_config_scope.rules %}
                - type: "{{ rule.type }}"
                  enabled: {{ rule.enabled }}
                  target: "{{ rule.target }}"
                  field: "{{ rule.field }}"
                  prefix: "{{ rule.prefix }}"
                  filters:
                    {%- for filter in rule.get("filters", []) %}
                    - field: "{{ filter.field }}"
                      value: "{{ filter.value }}"
                      type: "{{ filter.type }}"
                    {%- endfor%}
                {%- endfor %}
{%- endif %}

{% if attribute_config is defined %}
      - name: "{{ attribute_config.name }}"
        config:
          {%- if attribute_config.as_string is defined %}
          as_string:
            keys:
              {%- for key in attribute_config.as_string %}
              - "{{ key }}"
              {%- endfor %}
          {%- endif %}
          {%- if attribute_config.as_int is defined %}
          as_int:
            keys:
              {%- for key in attribute_config.as_int %}
              - "{{ key }}"
              {%- endfor %}
          {%- endif %}
          cut:
            {%- for config in attribute_config.cut %}
            - predicate_key: "{{ config.predicate_key }}"
              max_length: {{ config.max_length }}
              match:
                {%- for value in config.get("match", []) %}
                - "{{ value }}"
                {%- endfor %}
              keys:
                {%- for key in config.get("keys", []) %}
                - "{{ key }}"
                {%- endfor %}
            {%- endfor %}
          drop:
            {%- for config in attribute_config.drop %}
            - predicate_key: "{{ config.predicate_key }}"
              match:
                {%- for value in config.get("match", []) %}
                - "{{ value }}"
                {%- endfor %}
              keys:
                {%- for key in config.get("keys", []) %}
                - "{{ key }}"
                {%- endfor %}
            {%- endfor %}
{%- endif %}

{% if attribute_config_logs is defined %}
      - name: "{{ attribute_config_logs.name }}"
        config:
          {%- if attribute_config_logs.as_string is defined %}
          as_string:
            keys:
              {%- for key in attribute_config_logs.as_string %}
              - "{{ key }}"
              {%- endfor %}
          {%- endif %}
          {%- if attribute_config_logs.as_int is defined %}
          as_int:
            keys:
              {%- for key in attribute_config_logs.as_int %}
              - "{{ key }}"
              {%- endfor %}
          {%- endif %}
          cut:
            {%- for config in attribute_config_logs.cut %}
            - predicate_key: "{{ config.predicate_key }}"
              max_length: {{ config.max_length }}
              match:
                {%- for value in config.get("match", []) %}
                - "{{ value }}"
                {%- endfor %}
              keys:
                {%- for key in config.get("keys", []) %}
                - "{{ key }}"
                {%- endfor %}
            {%- endfor %}
          drop:
            {%- for config in attribute_config_logs.drop %}
            - predicate_key: "{{ config.predicate_key }}"
              match:
                {%- for value in config.get("match", []) %}
                - "{{ value }}"
                {%- endfor %}
              keys:
                {%- for key in config.get("keys", []) %}
                - "{{ key }}"
                {%- endfor %}
            {%- endfor %}
{%- endif %}


{% if sampler_config is defined %}
      - name: '{{ sampler_config.name }}'
        config:
          type: '{{ sampler_config.type }}'
          sampling_percentage: {{ sampler_config.sampling_percentage }}
{%- endif %}

{% if qps_config is defined %}
      - name: '{{ qps_config.name }}'
        config:
          type: '{{ qps_config.type }}'
          qps: {{ qps_config.qps }}
          burst: {{ qps_config.qps }}
{%- endif %}

{% if resource_filter_config is defined %}
      - name: '{{ resource_filter_config.name }}'
        config:
          assemble:
            {%- for as_config in  resource_filter_config.assemble %}
            - destination: '{{ as_config.destination }}'
              separator: '{{ as_config.separator }}'
              keys:
                {%- for key in as_config.get("keys", []) %}
                - '{{ key }}'
                {%- endfor %}
            {%- endfor %}
          drop:
            keys:
              {%- for drop_key in resource_filter_config.get("drop", {}).get("keys", []) %}
              - '{{ drop_key }}'
              {%- endfor %}
{%- endif %}

{% if resource_filter_config_logs is defined %}
      - name: '{{ resource_filter_config_logs.name }}'
        config:
          assemble:
            {%- for as_config in  resource_filter_config_logs.assemble %}
            - destination: '{{ as_config.destination }}'
              separator: '{{ as_config.separator }}'
              keys:
                {%- for key in as_config.get("keys", []) %}
                - '{{ key }}'
                {%- endfor %}
            {%- endfor %}
          drop:
            keys:
              {%- for drop_key in resource_filter_config_logs.get("drop", {}).get("keys", []) %}
              - '{{ drop_key }}'
              {%- endfor %}
{%- endif %}

{% if resource_filter_config_metrics is defined %}
      - name: '{{ resource_filter_config_metrics.name }}'
        config:
          {%- if resource_filter_config_metrics.get("assemble") %}
          assemble:
            {%- for as_config in resource_filter_config_metrics.assemble %}
            - destination: '{{ as_config.destination }}'
              separator: '{{ as_config.separator }}'
              keys:
                {%- for key in as_config.get("keys", []) %}
                - '{{ key }}'
                {%- endfor %}
            {%- endfor %}
          {%- endif %}
          {%- if resource_filter_config_metrics.get("drop") %}
          drop:
            keys:
              {%- for drop_key in resource_filter_config_metrics.drop.get("keys", []) %}
              - '{{ drop_key }}'
              {%- endfor %}
          {%- endif %}
          {%- if resource_filter_config_metrics.get("from_token") %}
          from_token:
            keys:
              {%- for token_key in resource_filter_config_metrics.from_token.get("keys", []) %}
              - '{{ token_key }}'
              {%- endfor %}
          {%- endif %}
          {%- if resource_filter_config_metrics.get("from_record") %}
          from_record:
            {%- for record_item in resource_filter_config_metrics.from_record %}
            - source: '{{ record_item.source }}'
              destination: '{{ record_item.destination }}'
            {%- endfor %}
          {%- endif %}
          {%- if resource_filter_config_metrics.get("from_cache") %}
          from_cache:
            key: '{{ resource_filter_config_metrics.from_cache.get("key", "") }}'
            cache_name: '{{ resource_filter_config_metrics.from_cache.get("cache_name", "") }}'
          {%- endif %}
{%- endif %}

{% if custom_service_config is defined %}
      - name: '{{ custom_service_config.name }}'
        config:
          rules:
            {%- for item in custom_service_config.rules %}
            - service: '{{ item.service }}'
              type: '{{ item.type }}'
              match_type: '{{ item.match_type }}'
              predicate_key: '{{ item.predicate_key }}'
{%- if item.match_key is defined %}
              match_key: '{{ item.match_key }}'
{%- endif %}
              kind: '{{ item.span_kind }}'
{%- if item.match_groups is defined %}
              match_groups:
{%- for group in item.match_groups %}
                - destination: '{{ group.destination }}'
{%- if group.source is defined %}
                  source: '{{ group.source }}'
{%- endif %}
{%- if group.const_val is defined %}
                  const_val: '{{ group.const_val }}'
{%- endif %}
{%- endfor %}
{%- endif %}
              rule:
{%- if item.rule.host is defined %}
                host:
                  operator: '{{ item.rule.host.operator }}'
                  value: '{{ item.rule.host.value }}'
{%- endif %}
{%- if item.rule.path is defined %}
                path:
                  operator: '{{ item.rule.path.operator }}'
                  value: '{{ item.rule.path.value }}'
{%- endif %}
{%- if item.rule.params is defined %}
                params:
{%- for param in item.rule.params %}
                  - name: '{{ param.name }}'
                    operator: '{{ param.operator }}'
                    value: '{{ param.value }}'
{%- endfor %}
{%- endif %}
{%- if item.rule.regex is defined %}
                regex: '{{ item.rule.regex }}'
{%- endif %}
{%- endfor %}
{%- endif %}

{% if method_filter_config is defined %}
      - name: '{{ method_filter_config.name }}'
        config:
          drop_span:
            rules:
              {%- for item in method_filter_config.get("drop_span", {}).get("rules", []) %}
              - predicate_key: '{{ item.predicate_key }}'
                kind: '{{ item.span_kind }}'
                match:
                  op: '{{ item.match.op }}'
                  value: '{{ item.match.value }}'
              {%- endfor %}
{%- endif %}

{% if service_configs is defined %}
service:
{%- for service_config in service_configs %}
  - id: '{{ service_config.unique_key }}'
    processor:
{% if service_config.apdex_config is defined %}
      - name: '{{ service_config.apdex_config.name }}'
        config:
          calculator:
            type: '{{ service_config.apdex_config.type }}'
          rules:
            {%- for rule_config in service_config.apdex_config.rules %}
            - kind: '{{ rule_config.kind }}'
              predicate_key: '{{ rule_config.predicate_key }}'
              metric_name: '{{ rule_config.metric_name }}'
              destination: '{{ rule_config.destination }}'
              apdex_t: {{ rule_config.apdex_t }} # ms
            {%- endfor %}
{%- endif %}

{% if service_config.sampler_config is defined %}
      - name: '{{ service_config.sampler_config.name }}'
        config:
          type: '{{ service_config.sampler_config.type }}'
          sampling_percentage: {{ service_config.sampler_config.sampling_percentage }}
{%- endif %}
{%- endfor %}
{%- endif %}

{% if instance_configs is defined %}
instance:
{%- for instance_config in instance_configs %}
  - id: '{{ instance_config.id }}'
    processor:
{% if instance_config.apdex_config is defined %}
      - name: '{{ instance_config.apdex_config.name }}'
        config:
          calculator:
            type: '{{ instance_config.apdex_config.type }}'
          rules:
            {%- for rule_config in instance_config.apdex_config.rules %}
            - kind: '{{ rule_config.kind }}'
              predicate_key: '{{ rule_config.predicate_key }}'
              metric_name: '{{ rule_config.metric_name }}'
              destination: '{{ rule_config.destination }}'
              apdex_t: {{ rule_config.apdex_t }} # ms
            {%- endfor %}
{%- endif %}

{% if instance_config.sampler_config is defined %}
      - name: '{{ instance_config.sampler_config.name }}'
        config:
          type: '{{ instance_config.sampler_config.type }}'
          sampling_percentage: {{ instance_config.sampler_config.sampling_percentage }}
{%- endif %}
{%- endfor %}
{%- endif %}
