type: 'subconfig'
token: '{{ bk_data_token }}'
bk_biz_id: {{ bk_biz_id }}
bk_app_name: {{ bk_app_name }}
default:
  processor:
{% if apdex_config is defined %}
      # ApdexCalculator: 健康度状态计算器
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

{% if sampler_config is defined %}
      # Sampler: 采样处理器
      - name: '{{ sampler_config.name }}'
        config:
          type: '{{ sampler_config.type }}'
          sampling_percentage: {{ sampler_config.sampling_percentage }}
{%- endif %}

{% if qps_config is defined %}
      # Qps: Qps限流
      - name: '{{ qps_config.name }}'
        config:
          type: '{{ qps_config.type }}'
          qps: {{ qps_config.qps }}
          burst: {{ qps_config.qps }}
{%- endif %}

{% if resource_filter_config is defined %}
      # ResourceFilter: 资源过滤处理器
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

{% if custom_service_config is defined %}
      # ServiceDiscover: 服务发现处理器
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
                - source: '{{ group.source }}'
                  destination: '{{ group.destination }}'
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

{% if service_configs is defined %}
service:
{%- for service_config in service_configs %}
  - id: '{{ service_config.unique_key }}'
    processor:
{% if service_config.apdex_config is defined %}
      # ApdexCalculator: 健康度状态计算器
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
      # Sampler: 采样处理器
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
      # ApdexCalculator: 健康度状态计算器
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
      # Sampler: 采样处理器
      - name: '{{ instance_config.sampler_config.name }}'
        config:
          type: '{{ instance_config.sampler_config.type }}'
          sampling_percentage: {{ instance_config.sampler_config.sampling_percentage }}
{%- endif %}

{%- endfor %}

{%- endif %}
